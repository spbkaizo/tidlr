// Command tidlr scrapes AnyDecentMusic for new albums, downloads them lossless
// from Tidal via tiddl, and converts them to ALAC in a configured folder.
//
// Usage:
//
//	tidlr scrape          Fetch ADM "Just in" and enqueue new albums.
//	tidlr run             Download + convert all pending queue items.
//	tidlr sync            scrape then run (the everyday command).
//	tidlr retry           Requeue failed items, then run.
//	tidlr status          Show queue state counts.
//
// Flags: -config <path>  (defaults to ./config.toml if present).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/config"
	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/pipeline"
	"github.com/simonb/tidlr/internal/queue"
	"github.com/simonb/tidlr/internal/tidal"
)

// version is set at build time via -ldflags "-X main.version=...". It defaults
// to "dev" for local/unstamped builds.
var version = "dev"

func main() {
	log.SetFlags(log.Ltime)

	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	force := flag.Bool("force", false, "re-download albums already in the library, overwriting files")
	since := flag.String("since", "", "scrape all albums added on/after this date (DDMMYY or DD/MM/YYYY)")
	playlist := flag.String("playlist", "", "download a Tidal playlist by URL or UUID into <output>/playlist/<name>")
	album := flag.String("album", "", "download a Tidal album by URL or ID into <output>/<artist>/<album>")
	track := flag.String("track", "", "download individual Tidal tracks by URL or ID into <output>/tracks")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Printf("tidlr %s\n", version)
		return
	}

	// --playlist, --album and --track are standalone operations: download and exit.
	if *playlist != "" || *album != "" || *track != "" {
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch {
		case *playlist != "":
			mustPlaylist(ctx, cfg, *playlist)
		case *album != "":
			mustAlbum(ctx, cfg, *album)
		default:
			mustTrack(ctx, cfg, *track)
		}
		return
	}

	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		log.Fatalf("creating work dir: %v", err)
	}

	q, err := queue.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("queue: %v", err)
	}
	defer q.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sinceTime, err := parseSince(*since)
	if err != nil {
		log.Fatalf("invalid --since %q: %v", *since, err)
	}

	switch flag.Arg(0) {
	case "version":
		fmt.Printf("tidlr %s\n", version)
	case "login":
		mustLogin(ctx, cfg)
	case "scrape":
		mustScrape(ctx, q, sinceTime, *force)
	case "run":
		mustRun(ctx, cfg, q)
	case "sync":
		mustScrape(ctx, q, sinceTime, *force)
		mustRun(ctx, cfg, q)
	case "retry":
		n, err := q.RequeueFailed()
		if err != nil {
			log.Fatalf("retry: %v", err)
		}
		log.Printf("requeued %d failed item(s)", n)
		mustRun(ctx, cfg, q)
	case "status":
		mustStatus(q)
	default:
		usage()
		os.Exit(2)
	}
}

// parseSince accepts DDMMYY (e.g. 010226) or DD/MM/YYYY. Empty => zero time.
func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"020106", "02/01/2006", "02012006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected DDMMYY or DD/MM/YYYY")
}

func mustScrape(ctx context.Context, q *queue.Queue, since time.Time, force bool) {
	client := adm.New()

	var releases []adm.Release
	var err error
	if since.IsZero() {
		// Default: just the recent "Just in" page.
		releases, err = client.JustOut(ctx)
	} else {
		// Walk the dated main chart back to `since`.
		releases, err = client.Since(ctx, since, 0)
	}
	if err != nil {
		log.Fatalf("scrape: %v", err)
	}

	added, err := q.Enqueue(releases, force)
	if err != nil {
		log.Fatalf("enqueue: %v", err)
	}
	if since.IsZero() {
		log.Printf("scraped %d releases, %d enqueued", len(releases), added)
	} else {
		log.Printf("scraped %d releases since %s, %d enqueued", len(releases), since.Format("2006-01-02"), added)
	}
}

func mustRun(ctx context.Context, cfg config.Config, q *queue.Queue) {
	auth, err := tidal.LoadAuth(cfg.AuthPath())
	if err != nil {
		log.Fatalf("Tidal auth: %v (run `tidlr login`)", err)
	}
	client := tidal.New(auth)
	// Refresh only when the token is expired/near-expiry, so a long batch
	// doesn't fail mid-run. The API client also self-refreshes on a 401, so
	// this is best-effort: on failure we continue.
	if auth.NeedsRefresh() {
		if err := auth.Refresh(ctx, client.HTTP); err != nil {
			log.Printf("warning: token refresh failed (%v); continuing with existing token", err)
		} else {
			log.Printf("refreshed Tidal auth token")
		}
	}

	// Recover items left mid-flight by a previous crash or hard kill.
	if n, err := q.RequeueStale(); err != nil {
		log.Fatalf("recovering stale items: %v", err)
	} else if n > 0 {
		log.Printf("recovered %d stale item(s) from a prior run", n)
	}

	dl := &downloader.Native{
		Matcher:    &tidal.Matcher{Client: client},
		Downloader: &tidal.Downloader{Client: client, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads},
		Quality:    cfg.Quality,
		BaseDir:    cfg.DownloadDir(),
	}
	conv := &convert.Converter{
		FFmpegBin: cfg.FFmpegBin,
		OutputDir: cfg.OutputDir,
		KeepFLAC:  cfg.KeepFLAC,
	}
	p := &pipeline.Pipeline{
		Queue:           q,
		Downloader:      dl,
		Converter:       conv,
		DownloadWorkers: cfg.DownloadWorkers,
		ConvertWorkers:  cfg.ConvertWorkers,
		Log:             log.Default(),
	}
	if err := p.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("run: %v", err)
	}
	mustStatus(q)
}

// authedClient loads the Tidal token and returns a ready client, refreshing the
// token if it is near expiry.
func authedClient(ctx context.Context, cfg config.Config) *tidal.Client {
	auth, err := tidal.LoadAuth(cfg.AuthPath())
	if err != nil {
		log.Fatalf("Tidal auth: %v (run `tidlr login`)", err)
	}
	client := tidal.New(auth)
	if auth.NeedsRefresh() {
		if err := auth.Refresh(ctx, client.HTTP); err != nil {
			log.Printf("warning: token refresh failed (%v); continuing", err)
		}
	}
	return client
}

// playlistUUIDRe matches a Tidal playlist UUID (with or without a URL wrapper).
var playlistUUIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// albumIDRe matches a Tidal album id in a URL (…/album/<id>…) or a bare number.
var albumIDRe = regexp.MustCompile(`(?:album/)?(\d+)`)

// mustPlaylist downloads a Tidal playlist and converts it to ALAC under
// <output_dir>/playlist/<name>/.
func mustPlaylist(ctx context.Context, cfg config.Config, arg string) {
	uuid := playlistUUIDRe.FindString(arg)
	if uuid == "" {
		log.Fatalf("could not find a playlist UUID in %q", arg)
	}

	client := authedClient(ctx, cfg)
	dl := &tidal.Downloader{Client: client, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads, Log: log.Default()}

	// Download into a scratch dir, then convert each track to ALAC in the final
	// playlist directory.
	scratch := filepath.Join(cfg.DownloadDir(), "playlist")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		log.Fatalf("scratch dir: %v", err)
	}
	log.Printf("downloading playlist %s ...", uuid)
	res, err := dl.DownloadPlaylist(ctx, uuid, cfg.Quality, scratch)
	if err != nil {
		log.Fatalf("playlist download: %v", err)
	}
	name := filepath.Base(res.Dir)
	log.Printf("downloaded %d tracks; converting to ALAC ...", res.Tracks)

	conv := &convert.Converter{FFmpegBin: cfg.FFmpegBin, OutputDir: filepath.Join(cfg.OutputDir, "playlist"), KeepFLAC: cfg.KeepFLAC}
	out, err := conv.ConvertFlat(ctx, name, res.Dir, res.Lossless)
	if err != nil {
		log.Fatalf("playlist convert: %v", err)
	}
	os.RemoveAll(res.Dir)
	log.Printf("done: playlist %q -> %s", name, out)
}

// mustAlbum downloads one or more Tidal albums (space/comma-separated URLs or
// ids) and converts each to ALAC under <output_dir>/<artist>/<album>/. A single
// album failing is logged and does not abort the rest.
func mustAlbum(ctx context.Context, cfg config.Config, arg string) {
	ids := parseAlbumIDs(arg)
	if len(ids) == 0 {
		log.Fatalf("could not find any album id in %q", arg)
	}

	client := authedClient(ctx, cfg)
	dl := &tidal.Downloader{Client: client, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads, Log: log.Default()}
	conv := &convert.Converter{FFmpegBin: cfg.FFmpegBin, OutputDir: cfg.OutputDir, KeepFLAC: cfg.KeepFLAC}
	scratch := filepath.Join(cfg.DownloadDir(), "album")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		log.Fatalf("scratch dir: %v", err)
	}

	ok, failed := 0, 0
	for i, albumID := range ids {
		if ctx.Err() != nil {
			break
		}
		log.Printf("[%d/%d] downloading album %d ...", i+1, len(ids), albumID)
		if err := downloadOneAlbum(ctx, cfg, dl, conv, scratch, albumID); err != nil {
			failed++
			log.Printf("[%d/%d] FAILED album %d: %v", i+1, len(ids), albumID, err)
			continue
		}
		ok++
	}
	log.Printf("albums: %d done, %d failed of %d", ok, failed, len(ids))
}

// downloadOneAlbum downloads and converts a single album; errors are returned
// (not fatal) so a batch can continue.
func downloadOneAlbum(ctx context.Context, cfg config.Config, dl *tidal.Downloader, conv *convert.Converter, scratch string, albumID int64) error {
	res, err := dl.DownloadAlbum(ctx, albumID, cfg.Quality, scratch)
	if err != nil {
		return err
	}
	// res.Dir is <scratch>/<Artist>/<Album>; deliver to <output>/<Artist>/<Album>.
	artist := filepath.Base(filepath.Dir(res.Dir))
	albumName := filepath.Base(res.Dir)
	out, err := conv.Convert(ctx, adm.Release{Artist: artist, Album: albumName}, res.Dir, res.Lossless)
	if err != nil {
		return err
	}
	os.RemoveAll(res.Dir)
	log.Printf("done: album %q — %q -> %s", artist, albumName, out)
	return nil
}

// mustTrack downloads one or more individual Tidal tracks (space/comma-separated
// URLs or ids) and converts them to ALAC under <output_dir>/tracks/. Standalone
// tracks have no album directory to live in, so they are filed together and
// named "<Artist> - <Title>".
func mustTrack(ctx context.Context, cfg config.Config, arg string) {
	ids := parseTrackIDs(arg)
	if len(ids) == 0 {
		log.Fatalf("could not find any track id in %q", arg)
	}

	client := authedClient(ctx, cfg)
	dl := &tidal.Downloader{Client: client, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads, Log: log.Default()}

	scratch := filepath.Join(cfg.DownloadDir(), "track")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		log.Fatalf("scratch dir: %v", err)
	}
	log.Printf("downloading %d track(s) ...", len(ids))
	res, err := dl.DownloadTracks(ctx, ids, cfg.Quality, scratch)
	if err != nil {
		log.Fatalf("track download: %v", err)
	}

	conv := &convert.Converter{FFmpegBin: cfg.FFmpegBin, OutputDir: cfg.OutputDir, KeepFLAC: cfg.KeepFLAC}
	out, err := conv.ConvertFlat(ctx, "tracks", res.Dir, res.Lossless)
	if err != nil {
		log.Fatalf("track convert: %v", err)
	}
	os.RemoveAll(res.Dir)
	log.Printf("done: %d/%d track(s) -> %s", res.Tracks, len(ids), out)
}

// trackIDRe matches a Tidal track id in a URL (…/track/<id>…) or a bare number.
var trackIDRe = regexp.MustCompile(`(?:track/)?(\d+)`)

// parseTrackIDs extracts all track ids from a string of space/comma-separated
// URLs or bare ids.
func parseTrackIDs(arg string) []int64 {
	var ids []int64
	for _, field := range strings.FieldsFunc(arg, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\t' }) {
		if m := trackIDRe.FindStringSubmatch(field); m != nil {
			if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// parseAlbumIDs extracts all album ids from a string of space/comma-separated
// URLs or bare ids.
func parseAlbumIDs(arg string) []int64 {
	var ids []int64
	for _, field := range strings.FieldsFunc(arg, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\t' }) {
		if m := albumIDRe.FindStringSubmatch(field); m != nil {
			if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// mustLogin runs the Tidal device-authorization flow and stores the token.
func mustLogin(ctx context.Context, cfg config.Config) {
	if auth, err := tidal.LoadAuth(cfg.AuthPath()); err == nil && auth.Token != "" {
		log.Printf("already logged in")
		return
	}
	_, err := tidal.Login(ctx, nil, cfg.AuthPath(), func(url string) {
		fmt.Printf("\nGo to %s and approve access, then wait...\n\n", url)
	})
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	log.Printf("logged in; token saved to %s", cfg.AuthPath())
}

func mustStatus(q *queue.Queue) {
	counts, err := q.Counts()
	if err != nil {
		log.Fatalf("status: %v", err)
	}
	// Every state must be listed: total is the sum of these, so an omitted
	// state would silently vanish from the count.
	order := []queue.State{
		queue.StatePending, queue.StateDownloading, queue.StateConverting,
		queue.StateDone, queue.StateFailed, queue.StateSkipped,
	}
	fmt.Println("queue:")
	total := 0
	for _, s := range order {
		fmt.Printf("  %-12s %d\n", s, counts[s])
		total += counts[s]
	}
	fmt.Printf("  %-12s %d\n", "total", total)
}

func usage() {
	fmt.Fprintf(os.Stderr, `tidlr - Tidal -> ALAC pipeline

Usage:
  tidlr [flags] <command>

Commands:
  login     Authenticate with Tidal (device-code flow); needed once
  scrape    Enqueue new albums from ADM (uses --since if given, else "Just in")
  run       Download + convert all pending queue items
  sync      scrape, then run  (the everyday command)
  retry     Requeue failed items, then run
  status    Show queue state counts
  version   Print the tidlr version

Flags:
  -config path    TOML config file (default config.toml)
  -since DATE     Scrape all albums added on/after DATE (DDMMYY or DD/MM/YYYY),
                  walking ADM's dated chart back in time. Without it, only the
                  recent "Just in" page is scraped.
  -force          Re-download albums already in the library, overwriting files.
  -playlist URL   Download a Tidal playlist (URL or UUID) as ALAC into
                  <output_dir>/playlist/<name>/. Standalone; ignores other args.
  -album URL      Download a Tidal album (URL or ID) as ALAC into
                  <output_dir>/<artist>/<album>/. Standalone; ignores other args.
  -track URL      Download individual Tidal tracks (URLs or IDs, space/comma-
                  separated) as ALAC into <output_dir>/tracks/. Standalone.

Examples:
  tidlr sync                 # grab the latest "Just in" albums
  tidlr --since 010226 sync  # grab everything added since 1 Feb 2026
  tidlr --force --since 010226 sync  # re-download that range from scratch
  tidlr --playlist https://tidal.com/playlist/f98d7491-...  # download a playlist
  tidlr --album https://tidal.com/album/540168117  # download a single album
  tidlr --track https://tidal.com/track/113302335  # download a single track
`)
}
