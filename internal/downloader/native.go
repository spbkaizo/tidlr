package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/tidal"
)

// Native downloads albums from Tidal using the in-process Go Tidal client — no
// external tiddl process. It matches an ADM release to the best Tidal album
// edition, then downloads its tracks.
type Native struct {
	Matcher    *tidal.Matcher
	Downloader *tidal.Downloader
	Quality    string // low|normal|high|max
	// BaseDir is the scratch root; each album downloads into a unique subdir so
	// the converter can find its files unambiguously.
	BaseDir string

	// Progress, if set, renders a live per-track display for each album. It is
	// only safe to set when albums are downloaded serially (DownloadWorkers ==
	// 1), because the callbacks it installs live on the shared tidal.Downloader
	// and a second concurrent album would interleave rows into the same
	// display. The pipeline enforces that; see cmd/tidlr.
	Progress AlbumProgress
}

// AlbumProgress renders a live display for one album download. Begin is called
// before the album's tracks are fetched and returns the callbacks the tidal
// downloader reports into, plus a done func to tear the display down.
type AlbumProgress interface {
	Begin(artist, album string) (onAlbumStart func(totalTracks int), onTrackStart func(tidal.Track) func(done, total int), done func())
}

// Download implements Downloader.
func (n *Native) Download(ctx context.Context, r adm.Release) (Result, error) {
	m, err := n.Matcher.Match(ctx, r.Artist, r.Album)
	if err != nil {
		if errors.Is(err, tidal.ErrNoMatch) {
			return Result{}, ErrNoMatch{Query: r.SearchQuery()}
		}
		return Result{}, fmt.Errorf("matching %q: %w", r.SearchQuery(), err)
	}

	// Isolate each album in its own scratch dir keyed by ReviewID.
	dest := filepath.Join(n.BaseDir, strconv.Itoa(r.ReviewID))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating scratch dir: %w", err)
	}

	// Install the live display for this album, if one is configured. The
	// callbacks are cleared again by finish() so a later album (or a failure
	// path) never reports into a torn-down display.
	if n.Progress != nil {
		onAlbum, onTrack, finish := n.Progress.Begin(r.Artist, r.Album)
		n.Downloader.OnAlbumStart = onAlbum
		n.Downloader.OnTrackStart = onTrack
		defer func() {
			finish()
			n.Downloader.OnAlbumStart = nil
			n.Downloader.OnTrackStart = nil
		}()
	}

	res, err := n.Downloader.DownloadAlbum(ctx, m.ID, n.Quality, dest)
	if err != nil {
		os.RemoveAll(dest) // clean partial download so a retry starts fresh
		return Result{}, fmt.Errorf("downloading %q: %w", r.SearchQuery(), err)
	}

	// m.Lossless (the edition's capability) and the actual delivered format
	// should agree; trust the downloader's observation of what landed on disk.
	return Result{AudioDir: res.Dir, Lossless: res.Lossless}, nil
}
