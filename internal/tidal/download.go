package tidal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simonb/tidlr/internal/fsname"
)

// trackQualities maps the CLI quality names to Tidal API quality levels, the
// same mapping tiddl uses (core/utils/const.py).
var trackQualities = map[string]string{
	"low":    "LOW",
	"normal": "HIGH",
	"high":   "LOSSLESS",
	"max":    "HI_RES_LOSSLESS",
}

// APIQuality resolves a CLI quality name (low|normal|high|max) to the API value,
// defaulting to HI_RES_LOSSLESS for unknown/empty input.
func APIQuality(name string) string {
	if q, ok := trackQualities[strings.ToLower(name)]; ok {
		return q
	}
	return "HI_RES_LOSSLESS"
}

// DownloadResult reports where an album's audio landed and whether it is
// lossless FLAC (vs. a lossy fallback container).
type DownloadResult struct {
	Dir      string // directory containing the track files (+ cover.jpg)
	Lossless bool
	Tracks   int
}

// Downloader downloads whole albums from Tidal to local disk.
type Downloader struct {
	Client    *Client
	FFmpegBin string      // for remuxing HiRes FLAC-in-mp4; defaults to "ffmpeg"
	Threads   int         // concurrent track downloads (default 4)
	Log       *log.Logger // optional; used to report per-track skips

	once   sync.Once
	fetchC *http.Client // long-timeout client for streaming track bodies
}

// fetchClient returns an HTTP client for streaming track/segment bodies. Unlike
// the API client (a tight 30s timeout is fine for small JSON responses), a full
// lossless track can take much longer under concurrency, so this client has no
// overall timeout and relies on the context for cancellation.
func (d *Downloader) fetchClient() *http.Client {
	d.once.Do(func() {
		d.fetchC = &http.Client{} // no Timeout; context-cancellable
	})
	return d.fetchC
}

// DownloadAlbum downloads every track of an album at the requested quality into
// <destDir>/<Artist>/<Album>/, plus a cover.jpg. quality is a CLI name
// (low|normal|high|max). lossless reports whether the delivered tracks are FLAC.
func (d *Downloader) DownloadAlbum(ctx context.Context, albumID int64, quality, destDir string) (DownloadResult, error) {
	apiQuality := APIQuality(quality)

	album, err := d.Client.GetAlbum(ctx, albumID)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("album %d: %w", albumID, err)
	}
	tracks, err := d.albumTracks(ctx, albumID, album.NumberOfTracks)
	if err != nil {
		return DownloadResult{}, err
	}
	if len(tracks) == 0 {
		return DownloadResult{}, fmt.Errorf("album %d has no tracks", albumID)
	}

	albumDir := filepath.Join(destDir, sanitize(album.ArtistName()), sanitize(album.Title))
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		return DownloadResult{}, err
	}

	// Fetch cover art (best effort; embedded later by the converter).
	if album.Cover != "" {
		if err := d.fetchCover(ctx, album.Cover, filepath.Join(albumDir, "cover.jpg")); err != nil {
			// Non-fatal: proceed without art rather than failing the album.
			_ = err
		}
	}

	threads := d.Threads
	if threads <= 0 {
		threads = 4
	}

	var (
		mu           sync.Mutex
		firstErr     error
		allLossless  = true
		anyDelivered bool
	)
	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup

	for _, tr := range tracks {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(tr Track) {
			defer wg.Done()
			defer func() { <-sem }()

			lossless, err := d.downloadTrack(ctx, tr, albumTrackMeta(tr, album), apiQuality, albumDir)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("track %q (%d): %w", tr.Title, tr.ID, err)
				}
				return
			}
			anyDelivered = true
			if !lossless {
				allLossless = false
			}
		}(tr)
	}
	wg.Wait()

	if firstErr != nil {
		return DownloadResult{}, firstErr
	}
	if !anyDelivered {
		return DownloadResult{}, fmt.Errorf("album %d: no tracks delivered", albumID)
	}
	return DownloadResult{Dir: albumDir, Lossless: allLossless, Tracks: len(tracks)}, nil
}

// albumTracks pages through the album's track listing.
func (d *Downloader) albumTracks(ctx context.Context, albumID int64, hint int) ([]Track, error) {
	var out []Track
	for offset := 0; ; offset += 100 {
		page, err := d.Client.GetAlbumItems(ctx, albumID, 100, offset)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.Type == "track" || it.Type == "" {
				out = append(out, it.Item)
			}
		}
		if offset+100 >= page.TotalNumberOfItems || len(page.Items) == 0 {
			break
		}
	}
	return out, nil
}

// DownloadPlaylist downloads every track of a playlist into <destDir>/<name>/,
// preserving playlist order via a numeric filename prefix. Unlike an album, each
// track keeps its own album art (embedded per-track during tagging) and album
// tag, since a playlist mixes tracks from many albums.
func (d *Downloader) DownloadPlaylist(ctx context.Context, uuid, quality, destDir string) (DownloadResult, error) {
	apiQuality := APIQuality(quality)

	pl, err := d.Client.GetPlaylist(ctx, uuid)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("playlist %s: %w", uuid, err)
	}
	tracks, err := d.playlistTracks(ctx, uuid)
	if err != nil {
		return DownloadResult{}, err
	}
	if len(tracks) == 0 {
		return DownloadResult{}, fmt.Errorf("playlist %q has no tracks", pl.Title)
	}

	plDir := filepath.Join(destDir, sanitize(pl.Title))
	if err := os.MkdirAll(plDir, 0o755); err != nil {
		return DownloadResult{}, err
	}
	// Scratch directory for per-track cover images (removed at the end).
	coverDir := filepath.Join(plDir, ".covers")
	os.MkdirAll(coverDir, 0o755)
	defer os.RemoveAll(coverDir)

	threads := d.Threads
	if threads <= 0 {
		threads = 4
	}

	// Width of the ordering prefix, e.g. 3 for 100+ tracks.
	width := len(strconv.Itoa(len(tracks)))

	var (
		mu          sync.Mutex
		delivered   int
		skipped     int
		allLossless = true
	)
	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup

	for i, tr := range tracks {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(position int, tr Track) {
			defer wg.Done()
			defer func() { <-sem }()

			meta := d.playlistTrackMeta(ctx, position, width, tr, coverDir)
			lossless, err := d.downloadTrack(ctx, tr, meta, apiQuality, plDir)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// One bad track (e.g. an unusual codec) shouldn't sink the whole
				// playlist; log and continue.
				skipped++
				if d.Log != nil {
					d.Log.Printf("playlist: skipped %q (%d): %v", tr.Title, tr.ID, err)
				}
				return
			}
			delivered++
			if !lossless {
				allLossless = false
			}
		}(i+1, tr)
	}
	wg.Wait()

	if delivered == 0 {
		return DownloadResult{}, fmt.Errorf("playlist %q: no tracks delivered (%d skipped)", pl.Title, skipped)
	}
	if skipped > 0 && d.Log != nil {
		d.Log.Printf("playlist: %d/%d tracks delivered, %d skipped", delivered, len(tracks), skipped)
	}
	return DownloadResult{Dir: plDir, Lossless: allLossless, Tracks: delivered}, nil
}

// playlistTracks pages through a playlist's track listing.
func (d *Downloader) playlistTracks(ctx context.Context, uuid string) ([]Track, error) {
	var out []Track
	for offset := 0; ; offset += 100 {
		page, err := d.Client.GetPlaylistItems(ctx, uuid, 100, offset)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.Type == "track" || it.Type == "" {
				out = append(out, it.Item)
			}
		}
		if offset+100 >= page.TotalNumberOfItems || len(page.Items) == 0 {
			break
		}
	}
	return out, nil
}

// playlistTrackMeta builds tag metadata for a playlist track, fetching its
// album cover into coverDir (best effort) so it can be embedded per-track.
func (d *Downloader) playlistTrackMeta(ctx context.Context, position, width int, tr Track, coverDir string) trackMeta {
	prefix := fmt.Sprintf("%0*d", width, position)
	meta := trackMeta{
		Title:       tr.Title,
		Artist:      tr.ArtistName(),
		Album:       tr.Album.Title,
		TrackNumber: position, // playlist ordering
		Date:        tr.Album.ReleaseDate,
		FileBase:    sanitize(prefix + " - " + tr.ArtistName() + " - " + tr.Title),
	}
	if tr.Album.Cover != "" {
		coverPath := filepath.Join(coverDir, strconv.FormatInt(tr.ID, 10)+".jpg")
		if err := d.fetchCover(ctx, tr.Album.Cover, coverPath); err == nil {
			meta.CoverPath = coverPath
		}
	}
	return meta
}

// trackMeta is the metadata written to a downloaded track. It is built either
// from an album (album downloads) or from the track's own fields (playlists,
// where every track belongs to a different album).
type trackMeta struct {
	Title       string
	Artist      string
	Album       string
	TrackNumber int
	Date        string
	// FileBase is the output filename (without extension).
	FileBase string
	// CoverPath, when set, is a jpeg embedded into the track during tagging.
	// Used for playlists where each track has its own album art (album
	// downloads embed a shared cover.jpg later, in the convert step).
	CoverPath string
}

func albumTrackMeta(tr Track, album Album) trackMeta {
	return trackMeta{
		Title:       tr.Title,
		Artist:      album.ArtistName(),
		Album:       album.Title,
		TrackNumber: tr.TrackNumber,
		Date:        album.ReleaseDate,
		FileBase:    trackFilename(tr),
	}
}

// downloadTrack fetches one track's stream, writes it, tags it, and remuxes
// HiRes FLAC-in-mp4 into a real .flac. Returns whether the delivered file is
// lossless FLAC (vs. a lossy fallback container such as Dolby Atmos).
func (d *Downloader) downloadTrack(ctx context.Context, tr Track, meta trackMeta, apiQuality, destDir string) (bool, error) {
	ts, err := d.Client.GetTrackStream(ctx, tr.ID, apiQuality)
	if err != nil {
		return false, err
	}
	info, err := ParseTrackStream(ts)
	if err != nil {
		return false, err
	}

	// We request stereo (immersiveaudio=false), so tracks come as: lossless FLAC
	// (.flac, or .m4a for HiRes FLAC-in-mp4 which we remux), stereo AAC (mp4a in
	// .m4a — a normal lossy fallback the ipod muxer accepts), or — only when an
	// album is genuinely stereo-less — Dolby Atmos eac3, which the .m4a/ipod
	// muxer refuses, so that goes to .mp4.
	lossless := info.Extension == ".flac" || strings.EqualFold(ts.AudioQuality, "HI_RES_LOSSLESS")
	finalExt := info.Extension
	if lossless {
		finalExt = ".flac"
	} else if info.Dolby {
		finalExt = ".mp4"
	}

	raw := filepath.Join(destDir, meta.FileBase+".raw"+info.Extension)
	if err := d.fetchSegments(ctx, info.URLs, raw); err != nil {
		return false, err
	}

	// Tag while transcoding the container: raw stream bytes carry no metadata
	// (unlike tiddl, which embedded tags via mutagen), so we write title/artist/
	// album/track with ffmpeg. `-c copy` keeps the audio bit-exact, and it also
	// remuxes HiRes FLAC-in-mp4 into a real .flac. The ipod muxer rejects an
	// attached picture alongside a Dolby eac3 stream, so skip the cover there.
	if info.Dolby {
		meta.CoverPath = ""
	}
	final := filepath.Join(destDir, meta.FileBase+finalExt)
	if err := d.tag(ctx, raw, final, meta); err != nil {
		os.Remove(raw)
		return false, fmt.Errorf("tagging track: %w", err)
	}
	os.Remove(raw)

	return lossless, nil
}

// tag copies src to dst with `-c copy` (bit-exact audio) while writing track
// metadata, optionally embedding a cover image. Works for .flac and .m4a.
func (d *Downloader) tag(ctx context.Context, src, dst string, meta trackMeta) error {
	bin := d.FFmpegBin
	if bin == "" {
		bin = "ffmpeg"
	}
	args := []string{"-y", "-nostdin", "-i", src}
	if meta.CoverPath != "" {
		args = append(args, "-i", meta.CoverPath,
			"-map", "0:a", "-map", "1:v",
			"-c", "copy", "-disposition:v", "attached_pic")
	} else {
		args = append(args, "-c", "copy")
	}
	args = append(args,
		"-metadata", "title="+meta.Title,
		"-metadata", "artist="+meta.Artist,
		"-metadata", "album_artist="+meta.Artist,
		"-metadata", "album="+meta.Album,
		"-metadata", "track="+strconv.Itoa(meta.TrackNumber),
	)
	if meta.Date != "" {
		args = append(args, "-metadata", "date="+meta.Date)
	}
	args = append(args, dst)

	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, tailLines(string(out)))
	}
	return nil
}

// fetchSegments downloads and concatenates the segment URLs into path, retrying
// the whole fetch a few times on transient network errors (segment drops from
// e.g. a firewall interrupting a connection). Writes via a unique temp file so
// concurrent downloads can never collide, and cleans it up on failure.
func (d *Downloader) fetchSegments(ctx context.Context, urls []string, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	const attempts = 4
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := d.fetchSegmentsOnce(ctx, urls, path)
		if err == nil {
			return nil
		}
		lastErr = err

		// A rate-limited CDN needs a real pause, not the short linear backoff
		// that suits a dropped connection.
		wait := time.Duration(attempt) * time.Second
		var rl rateLimitedError
		if errors.As(err, &rl) {
			wait = rateLimitWait(rl.RetryAfter)
			if d.Log != nil {
				d.Log.Printf("rate limited fetching segments, waiting %s before retry %d/%d",
					wait.Round(time.Second), attempt+1, attempts)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func (d *Downloader) fetchSegmentsOnce(ctx context.Context, urls []string, path string) error {
	// A unique temp name per attempt guarantees no collision between concurrent
	// track downloads and no stale ".part" from a prior failed attempt.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	success := false
	defer func() {
		if !success {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		resp, err := d.fetchClient().Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			return rateLimitedError{Path: "segment", RetryAfter: retryAfter}
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("segment GET: HTTP %d", resp.StatusCode)
		}
		_, err = io.Copy(tmp, resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	success = true
	return nil
}

// fetchCover downloads the album cover jpeg (1280px) to path.
func (d *Downloader) fetchCover(ctx context.Context, coverUID, path string) error {
	url := "https://resources.tidal.com/images/" + strings.ReplaceAll(coverUID, "-", "/") + "/1280x1280.jpg"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := d.fetchClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cover GET: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// trackFilename builds a stable, sortable file base like "01 - Title" (or
// "1-01 - Title" for multi-disc albums, using the volume number).
func trackFilename(tr Track) string {
	num := fmt.Sprintf("%02d", tr.TrackNumber)
	if tr.VolumeNumber > 1 {
		num = strconv.Itoa(tr.VolumeNumber) + "-" + num
	}
	return sanitize(num + " - " + tr.Title)
}

// sanitize makes a string safe as a path component.
func sanitize(s string) string { return fsname.Sanitize(s) }

func tailLines(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}
