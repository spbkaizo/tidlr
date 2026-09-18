// Command tidlr scrapes AnyDecentMusic for new albums, downloads them lossless
// from Tidal via tiddl, and converts them to ALAC in a configured folder.
//
// Usage:
//
//	tidlr scrape          Fetch ADM "Just in" and enqueue new albums.
//	tidlr run             Download + convert all pending queue items.
//	tidlr sync            requeue failed items, scrape, then run (the everyday command).
//	tidlr retry           Requeue failed items, then run (no scrape).
//	tidlr status          Show queue state counts.
//
// Flags: -config <path>  (defaults to ./config.toml if present).
//
//	-v, -version  Print the version and exit.
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
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"
	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/config"
	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/pipeline"
	"github.com/simonb/tidlr/internal/queue"
	"github.com/simonb/tidlr/internal/tidal"
)

// version is set at build time via -ldflags "-X main.version=..." for tagged
// release builds. It stays empty for local/unstamped builds, where
// versionString() synthesises a "dev" version from the embedded VCS stamp
// instead.
var version = ""

// baseVersion is the last released tag the tree is built on, set at build time
// via -ldflags "-X main.baseVersion=$(git describe --tags --abbrev=0)" (the
// Makefile and CI do this). It is only reported for dev builds, to say which
// release they are based on. When unset, versionString falls back to the module
// version the toolchain embeds, which is populated for `go install`-style
// builds but reads "(devel)" for a plain local `go build`.
var baseVersion = ""

// versionString renders the full version banner. Tagged builds report the tag;
// unstamped local builds report "dev" plus the commit date and short hash that
// the Go toolchain embeds in the binary, and the release they are based on.
//
// The date is the commit time (not the build time), so two builds of the same
// tree always report the same version.
func versionString() string {
	var b strings.Builder
	info, _ := debug.ReadBuildInfo()

	if version != "" {
		fmt.Fprintf(&b, "tidlr %s", version)
	} else {
		fmt.Fprint(&b, "tidlr dev")
		base := baseVersion
		if base == "" && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
			base = info.Main.Version
		}
		if base != "" {
			fmt.Fprintf(&b, " (based on %s)", base)
		}
	}
	b.WriteByte('\n')

	rev, commitTime, dirty := vcsInfo(info)
	if rev != "" {
		short := rev
		if len(short) > 7 {
			short = short[:7]
		}
		if dirty {
			short += "-dirty"
		}
		if commitTime != "" {
			fmt.Fprintf(&b, "  commit:  %s (%s)\n", short, commitTime)
		} else {
			fmt.Fprintf(&b, "  commit:  %s\n", short)
		}
	}
	if info != nil {
		fmt.Fprintf(&b, "  go:      %s %s/%s\n", info.GoVersion, runtime.GOOS, runtime.GOARCH)
	}
	return b.String()
}

// vcsInfo pulls the revision, commit date (YYYY-MM-DD) and dirty flag out of
// the build info the toolchain embeds. All three are absent when the binary was
// built outside a git checkout (e.g. `go build` from a module cache), in which
// case the caller simply omits the line.
func vcsInfo(info *debug.BuildInfo) (rev, date string, dirty bool) {
	if info == nil {
		return "", "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			// Recorded as RFC3339; report just the date.
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				date = t.Format("2006-01-02")
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, date, dirty
}

// stringList is a flag.Value that accumulates every occurrence of a flag rather
// than keeping only the last, so -album A -album B downloads both. Each value
// may itself hold several space/comma-separated ids; the downstream parsers
// split them, so the values are simply joined with a space.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, " ") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func (l *stringList) joined() string { return strings.Join(*l, " ") }

// joinArgs appends any bare trailing arguments to a flag's accumulated values.
func joinArgs(flagged, rest string) string {
	if rest == "" {
		return flagged
	}
	if flagged == "" {
		return rest
	}
	return flagged + " " + rest
}

func main() {
	log.SetFlags(log.Ltime)

	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	force := flag.Bool("force", false, "re-download albums already in the library, overwriting files")
	since := flag.String("since", "", "scrape all albums added on/after this date (DDMMYY or DD/MM/YYYY)")
	playlist := flag.String("playlist", "", "download a Tidal playlist by URL or UUID into <output>/playlist/<name>")
	var album, track stringList
	flag.Var(&album, "album", "download a Tidal album by URL or ID into <output>/<artist>/<album> (repeatable)")
	flag.Var(&track, "track", "download individual Tidal tracks by URL or ID into <output>/tracks (repeatable)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version and exit (shorthand for -version)")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Print(versionString())
		return
	}

	// --playlist, --album and --track are standalone operations: download and exit.
	if *playlist != "" || len(album) > 0 || len(track) > 0 {
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// Bare trailing arguments belong to whichever download flag was given,
		// so `-album URL URL URL` takes all three rather than silently dropping
		// every URL after the first (Go's flag package binds only the value
		// immediately following -album; the rest arrive as positional args).
		rest := strings.Join(flag.Args(), " ")

		switch {
		case *playlist != "":
			mustPlaylist(ctx, cfg, *playlist)
		case len(album) > 0:
			mustAlbum(ctx, cfg, joinArgs(album.joined(), rest), *force)
		default:
			mustTrack(ctx, cfg, joinArgs(track.joined(), rest))
		}
		return
	}

	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}

	// `version` needs neither config nor queue; answer before either can fail.
	if flag.Arg(0) == "version" {
		fmt.Print(versionString())
		return
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
	case "login":
		mustLogin(ctx, cfg)
	case "scrape":
		mustScrape(ctx, q, sinceTime, *force)
	case "run":
		mustRun(ctx, cfg, q)
	case "sync":
		n, err := q.RequeueFailed()
		if err != nil {
			log.Fatalf("sync: %v", err)
		}
		if n > 0 {
			log.Printf("requeued %d failed item(s) from previous run", n)
		}
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

	// The live per-track display draws to a single terminal region, so it can
	// only render one album at a time. Downloading albums concurrently would
	// interleave rows from different albums into it, so the display forces
	// download_workers to 1: detail costs throughput, and the choice is the
	// user's. Set progress = false in the config to keep albums concurrent.
	//
	// Conversion still overlaps with downloading either way, and per-album
	// track concurrency (download_threads, default 8) is untouched — it is only
	// the number of *albums* in flight that drops.
	//
	// On a non-TTY there is no cursor to rewind, so the display draws nothing;
	// trading away concurrency for it would cost throughput and buy no output.
	downloadWorkers := cfg.DownloadWorkers
	quietDownloadStart := false
	runLog := log.Default()
	if cfg.Progress && isatty.IsTerminal(os.Stderr.Fd()) {
		pending := 0
		if counts, err := q.Counts(); err == nil {
			pending = counts[queue.StatePending]
		}
		prog := newAlbumProgress(cfg.DownloadThreads)
		quietDownloadStart = true
		// Route this run's logging through the display so concurrent convert
		// workers print above the live region instead of inside it.
		runLog = log.New(prog, "", log.Ltime)
		dl.Progress = &queueProgress{prog: prog, log: runLog, total: pending}
		if downloadWorkers != 1 {
			log.Printf("progress display on: downloading albums one at a time (set progress = false for %d concurrent)", downloadWorkers)
			downloadWorkers = 1
		}
	}
	conv := &convert.Converter{
		FFmpegBin: cfg.FFmpegBin,
		OutputDir: cfg.OutputDir,
		KeepFLAC:  cfg.KeepFLAC,
	}
	p := &pipeline.Pipeline{
		Queue:              q,
		Downloader:         dl,
		Converter:          conv,
		DownloadWorkers:    downloadWorkers,
		QuietDownloadStart: quietDownloadStart,
		ConvertWorkers:     cfg.ConvertWorkers,
		Log:                runLog,
	}
	if err := p.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("run: %v", err)
	}
	mustStatus(q)
}

// trackProgress renders one shared spinning progress bar across a batch of
// concurrently-downloading tracks. Tracks download in parallel (default 8 at
// once), so distinct per-track bars would tear on the same terminal line;
// instead the bar's label reflects whichever track most recently reported a
// segment, with a running count of tracks started.
type trackProgress struct {
	bar     *progressbar.ProgressBar
	mu      sync.Mutex
	started int
	total   int
}

func newTrackProgress(total int) *trackProgress {
	return &trackProgress{
		total: total,
		bar: progressbar.NewOptions(-1,
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionSpinnerType(11),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionClearOnFinish(),
		),
	}
}

// onTrackStart is a tidal.Downloader.OnTrackStart implementation.
func (p *trackProgress) onTrackStart(tr tidal.Track) func(done, total int) {
	p.mu.Lock()
	p.started++
	n := p.started
	p.mu.Unlock()

	return func(done, total int) {
		p.mu.Lock()
		defer p.mu.Unlock()
		label := fmt.Sprintf("track %d", n)
		if p.total > 0 {
			label = fmt.Sprintf("[%d/%d]", n, p.total)
		}
		p.bar.Describe(fmt.Sprintf("%s %s (segment %d/%d)", label, tr.Title, done, total))
		p.bar.Add(1)
	}
}

func (p *trackProgress) finish() {
	p.bar.Finish()
	p.bar.Close()
}

// albumProgress renders a live multi-line display for one album download: one
// row per in-flight track (up to Threads concurrent rows) showing its segment
// progress, plus a trailing album-wide row showing tracks completed. It
// redraws in place using ANSI cursor movement, so it only animates on a real
// terminal; on a non-TTY (redirected/CI logs) it falls back to printing each
// track's completion once, since there is no cursor to rewind.
type albumProgress struct {
	mu       sync.Mutex
	tty      bool
	total    int // total tracks in the album; set by onAlbumStart
	done     int
	rows     []albumProgressRow // fixed slots, one per concurrent track
	lastDraw int                // number of terminal lines the previous draw occupied
}

type albumProgressRow struct {
	active bool
	title  string
	done   int
	total  int
}

func newAlbumProgress(threads int) *albumProgress {
	if threads <= 0 {
		threads = 4
	}
	return &albumProgress{
		tty:  isatty.IsTerminal(os.Stderr.Fd()),
		rows: make([]albumProgressRow, threads),
	}
}

// onAlbumStart is a tidal.Downloader.OnAlbumStart implementation.
func (p *albumProgress) onAlbumStart(total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total = total
}

// logAbove prints a log line above the live display: it erases the drawn
// region, writes the line, then redraws. Convert workers run concurrently with
// downloads and log as they go, so without this their output would land inside
// the region and be overwritten by the next redraw's cursor rewind.
//
// It satisfies the io.Writer that a log.Logger writes into, so the pipeline's
// existing logging needs no changes.
func (p *albumProgress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tty {
		return os.Stderr.Write(b)
	}
	// Rewind over and clear the live region, so the log line takes its place.
	if p.lastDraw > 0 {
		fmt.Fprintf(os.Stderr, "\x1b[%dA", p.lastDraw)
		for i := 0; i < p.lastDraw; i++ {
			fmt.Fprint(os.Stderr, "\x1b[2K\n")
		}
		fmt.Fprintf(os.Stderr, "\x1b[%dA", p.lastDraw)
		p.lastDraw = 0
	}
	n, err := os.Stderr.Write(b)
	p.draw() // redraw the region below the line just written
	return n, err
}

// reset clears all per-album state so one albumProgress can be reused for the
// next album. lastDraw is deliberately left alone: finish() has already
// rewound the cursor, so the next draw starts from a clean slate.
func (p *albumProgress) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total = 0
	p.done = 0
	for i := range p.rows {
		p.rows[i] = albumProgressRow{}
	}
}

// queueProgress adapts albumProgress to downloader.AlbumProgress, so the ADM
// pipeline gets the same live display as -album. It is only installed when
// albums download serially; see mustRun.
type queueProgress struct {
	prog  *albumProgress
	log   *log.Logger // logs through prog, so the header sits above the display
	n     int         // albums started so far
	total int         // albums pending at the start of the run (0 if unknown)
}

// Begin implements downloader.AlbumProgress.
func (q *queueProgress) Begin(artist, album string) (func(int), func(tidal.Track) func(int, int), func()) {
	q.n++
	logger := q.log
	if logger == nil {
		logger = log.Default()
	}
	if q.total > 0 {
		logger.Printf("[%d/%d] downloading %s — %s ...", q.n, q.total, artist, album)
	} else {
		logger.Printf("[%d] downloading %s — %s ...", q.n, artist, album)
	}
	q.prog.reset()
	return q.prog.onAlbumStart, q.prog.onTrackStart, q.prog.finish
}

// onTrackStart is a tidal.Downloader.OnTrackStart implementation.
func (p *albumProgress) onTrackStart(tr tidal.Track) func(done, total int) {
	p.mu.Lock()
	slot := -1
	for i := range p.rows {
		if !p.rows[i].active {
			slot = i
			break
		}
	}
	if slot == -1 {
		// More concurrent tracks than rows (shouldn't happen: rows == Threads,
		// the actual concurrency cap); fall back to overwriting the first row
		// rather than losing the callback.
		slot = 0
	}
	p.rows[slot] = albumProgressRow{active: true, title: tr.Title}
	p.draw()
	p.mu.Unlock()

	return func(done, total int) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.rows[slot].done = done
		p.rows[slot].total = total
		if done >= total {
			p.rows[slot] = albumProgressRow{}
			p.done++
		}
		p.draw()
	}
}

// draw renders the current state. Must be called with mu held.
func (p *albumProgress) draw() {
	if !p.tty {
		return
	}
	// Move cursor up to the start of the previous draw, then redraw every line
	// (clearing each first, since a new line may be shorter than the old one).
	if p.lastDraw > 0 {
		fmt.Fprintf(os.Stderr, "\x1b[%dA", p.lastDraw)
	}
	lines := 0
	for _, r := range p.rows {
		fmt.Fprint(os.Stderr, "\x1b[2K")
		if r.active {
			fmt.Fprintf(os.Stderr, "  %-40s %s\n", truncate(r.title, 40), barString(r.done, r.total, 20))
		} else {
			fmt.Fprintln(os.Stderr)
		}
		lines++
	}
	fmt.Fprint(os.Stderr, "\x1b[2K")
	fmt.Fprintf(os.Stderr, "  album %s %d/%d tracks\n", barString(p.done, p.total, 20), p.done, p.total)
	lines++
	p.lastDraw = lines
}

// finish clears the live display (on a TTY) or, on a non-TTY, is a no-op since
// nothing was drawn to clear.
func (p *albumProgress) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tty || p.lastDraw == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\x1b[%dA", p.lastDraw)
	for i := 0; i < p.lastDraw; i++ {
		fmt.Fprint(os.Stderr, "\x1b[2K\n")
	}
	fmt.Fprintf(os.Stderr, "\x1b[%dA", p.lastDraw)
	p.lastDraw = 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func barString(done, total, width int) string {
	if total <= 0 {
		return "[" + strings.Repeat("-", width) + "]"
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
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
	prog := newTrackProgress(0) // total tracks unknown until fetched; shown as running count
	dl.OnTrackStart = prog.onTrackStart
	res, err := dl.DownloadPlaylist(ctx, uuid, cfg.Quality, scratch)
	prog.finish()
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
func mustAlbum(ctx context.Context, cfg config.Config, arg string, force bool) {
	ids := parseAlbumIDs(arg)
	if len(ids) == 0 {
		log.Fatalf("could not find any album id in %q", arg)
	}
	ids = dedupeIDs(ids)

	// The library record lives in the queue DB. A failure to open it is not
	// fatal: fall back to downloading everything, as before.
	var q *queue.Queue
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		log.Printf("warning: work dir: %v; not recording downloads", err)
	} else if opened, err := queue.Open(cfg.DBPath()); err != nil {
		log.Printf("warning: opening library: %v; not recording downloads", err)
	} else {
		q = opened
		defer q.Close()
	}

	client := authedClient(ctx, cfg)
	dl := &tidal.Downloader{Client: client, FFmpegBin: cfg.FFmpegBin, Threads: cfg.DownloadThreads, Log: log.Default()}
	conv := &convert.Converter{FFmpegBin: cfg.FFmpegBin, OutputDir: cfg.OutputDir, KeepFLAC: cfg.KeepFLAC}
	scratch := filepath.Join(cfg.DownloadDir(), "album")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		log.Fatalf("scratch dir: %v", err)
	}

	ok, failed, skipped := 0, 0, 0
	for i, albumID := range ids {
		if ctx.Err() != nil {
			break
		}
		// Skip what the library already has, unless --force says otherwise.
		if q != nil && !force {
			if artist, album, dir, have, err := q.TidalDownload(albumID); err != nil {
				log.Printf("warning: library lookup for %d: %v; downloading anyway", albumID, err)
			} else if have {
				skipped++
				log.Printf("[%d/%d] skipping album %d: already have %q — %q at %s (use -force to re-download)",
					i+1, len(ids), albumID, artist, album, dir)
				continue
			}
		}
		log.Printf("[%d/%d] downloading album %d ...", i+1, len(ids), albumID)
		out, artist, album, err := downloadOneAlbum(ctx, cfg, dl, conv, scratch, albumID)
		if err != nil {
			failed++
			log.Printf("[%d/%d] FAILED album %d: %v", i+1, len(ids), albumID, err)
			continue
		}
		ok++
		if q != nil {
			if err := q.MarkTidalDownloaded(albumID, artist, album, out); err != nil {
				log.Printf("warning: recording album %d in library: %v", albumID, err)
			}
		}
	}
	if skipped > 0 {
		log.Printf("albums: %d done, %d skipped, %d failed of %d", ok, skipped, failed, len(ids))
	} else {
		log.Printf("albums: %d done, %d failed of %d", ok, failed, len(ids))
	}
}

// dedupeIDs removes repeated album ids, preserving first-seen order, so the
// same album listed twice on one command line is fetched once.
func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// downloadOneAlbum downloads and converts a single album; errors are returned
// (not fatal) so a batch can continue.
func downloadOneAlbum(ctx context.Context, cfg config.Config, dl *tidal.Downloader, conv *convert.Converter, scratch string, albumID int64) (outDir, artist, albumName string, err error) {
	prog := newAlbumProgress(dl.Threads)
	dl.OnAlbumStart = prog.onAlbumStart
	dl.OnTrackStart = prog.onTrackStart
	res, err := dl.DownloadAlbum(ctx, albumID, cfg.Quality, scratch)
	prog.finish()
	if err != nil {
		return "", "", "", err
	}
	// res.Dir is <scratch>/<Artist>/<Album>; deliver to <output>/<Artist>/<Album>.
	artist = filepath.Base(filepath.Dir(res.Dir))
	albumName = filepath.Base(res.Dir)
	out, err := conv.Convert(ctx, adm.Release{Artist: artist, Album: albumName}, res.Dir, res.Lossless)
	if err != nil {
		return "", "", "", err
	}
	os.RemoveAll(res.Dir)
	log.Printf("done: album %q — %q -> %s", artist, albumName, out)
	return out, artist, albumName, nil
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
	prog := newTrackProgress(len(ids))
	dl.OnTrackStart = prog.onTrackStart
	res, err := dl.DownloadTracks(ctx, ids, cfg.Quality, scratch)
	prog.finish()
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
  sync      requeue failed items, scrape, then run  (the everyday command)
  retry     Requeue failed items, then run (no scrape)
  status    Show queue state counts
  version   Print the tidlr version

Flags:
  -v, -version    Print version (release tag, or for a dev build the commit
                  date, short hash and the release it is based on) and exit.
  -config path    TOML config file (default config.toml)
  -since DATE     Scrape all albums added on/after DATE (DDMMYY or DD/MM/YYYY),
                  walking ADM's dated chart back in time. Without it, only the
                  recent "Just in" page is scraped.
  -force          Re-download albums already in the library, overwriting files.
                  Applies to both the ADM queue and -album.
  -playlist URL   Download a Tidal playlist (URL or UUID) as ALAC into
                  <output_dir>/playlist/<name>/. Standalone; ignores other args.
  -album URL      Download a Tidal album (URL or ID) as ALAC into
                  <output_dir>/<artist>/<album>/. Takes any number of URLs/IDs:
                  repeat the flag, list them after it, or comma-separate them.
                  Albums already downloaded this way are skipped unless -force
                  is given. Standalone; ignores other args.
  -track URL      Download individual Tidal tracks (URLs or IDs, space/comma-
                  separated) as ALAC into <output_dir>/tracks/. Repeatable.
                  Standalone.

Examples:
  tidlr sync                 # grab the latest "Just in" albums
  tidlr --since 010226 sync  # grab everything added since 1 Feb 2026
  tidlr --force --since 010226 sync  # re-download that range from scratch
  tidlr --playlist https://tidal.com/playlist/f98d7491-...  # download a playlist
  tidlr --album https://tidal.com/album/540168117  # download a single album
  tidlr --album 540168117 522251328 533982947      # several albums in one go
  tidlr --album 540168117 --album 522251328        # the same, repeating the flag
  tidlr --force --album 540168117                  # re-download one we already have
  tidlr --track https://tidal.com/track/113302335  # download a single track
`)
}
