// Package pipeline wires the stages together: it drains the queue with bounded
// concurrency, downloading albums then converting them to ALAC. Download and
// convert have separate worker limits because they bottleneck on different
// resources (Tidal bandwidth vs. local CPU).
package pipeline

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/filing"
	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/organize"
	"github.com/simonb/tidlr/internal/queue"
)

// Converter turns a downloaded album into ALAC in the output folder
// (*convert.Converter).
type Converter interface {
	Convert(ctx context.Context, r adm.Release, srcDir string, lossless bool) (string, error)
}

// Organizer identifies, tags and files a converted album in the library
// (*organize.Organizer).
type Organizer interface {
	Organize(ctx context.Context, a organize.Album) (string, error)
	InLibrary(artist, title, year string) (string, bool)
}

var (
	_ Converter = (*convert.Converter)(nil)
	_ Organizer = (*organize.Organizer)(nil)
)

// Pipeline processes queued albums.
type Pipeline struct {
	Queue      *queue.Queue
	Downloader downloader.Downloader
	Converter  Converter

	DownloadWorkers int
	ConvertWorkers  int

	// QuietDownloadStart suppresses the per-album "downloading: …" line. Set it
	// when a live progress display already announces each album, so the two do
	// not print the same thing twice.
	QuietDownloadStart bool

	// Organizer, if set, identifies, tags and files each converted album in
	// the library (replacing Picard). Nil keeps albums where convert put them;
	// never set it to a nil *organize.Organizer, which is not a nil Organizer.
	Organizer Organizer
	// Force downloads albums the library already has (--force); otherwise
	// an album found there, loosely matched by name, is skipped.
	Force bool

	halted atomic.Bool // set when the library becomes unreachable
	halt   sync.Once

	Log *log.Logger
}

// Run drains all currently-pending items and returns when the queue has no more
// pending work (or ctx is cancelled). Downloaded albums are handed to the
// converter via an in-process channel so conversion overlaps with downloading.
func (p *Pipeline) Run(ctx context.Context) error {
	type job struct {
		item queue.Item
		res  downloader.Result
	}
	convertCh := make(chan job, p.ConvertWorkers*2)

	// Convert workers.
	var convWG sync.WaitGroup
	for i := 0; i < max(p.ConvertWorkers, 1); i++ {
		convWG.Add(1)
		go func() {
			defer convWG.Done()
			for j := range convertCh {
				p.runConvert(ctx, j.item, j.res)
			}
		}()
	}

	// Download workers pull pending items until none remain.
	var dlWG sync.WaitGroup
	for i := 0; i < max(p.DownloadWorkers, 1); i++ {
		dlWG.Add(1)
		go func() {
			defer dlWG.Done()
			for {
				if ctx.Err() != nil || p.halted.Load() {
					return
				}
				item, err := p.Queue.ClaimPending(queue.StateDownloading)
				if err != nil {
					p.logf("claim error: %v", err)
					return
				}
				if item == nil {
					return // no pending work left
				}
				if res, ok := p.runDownload(ctx, *item); ok {
					convertCh <- job{item: *item, res: res}
				}
			}
		}()
	}

	dlWG.Wait()
	close(convertCh)
	convWG.Wait()
	return ctx.Err()
}

func (p *Pipeline) runDownload(ctx context.Context, item queue.Item) (downloader.Result, bool) {
	r := adm.Release{ReviewID: item.ReviewID, Artist: item.Artist, Album: item.Album, ReviewURL: item.ReviewURL}
	if p.Organizer != nil && !p.Force {
		if dir, ok := p.Organizer.InLibrary(item.Artist, item.Album, ""); ok {
			p.logf("skipped (in library): %s — %s at %s", item.Artist, item.Album, dir)
			if err := p.Queue.MarkDownloaded(item.ReviewID, item.Artist, item.Album, dir); err != nil {
				p.logf("mark downloaded: %v", err)
			}
			if err := p.Queue.SetState(item.ReviewID, queue.StateDone); err != nil {
				p.logf("set done: %v", err)
			}
			return downloader.Result{}, false
		}
	}
	if !p.QuietDownloadStart {
		p.logf("downloading: %s — %s", item.Artist, item.Album)
	}

	res, err := p.Downloader.Download(ctx, r)
	if err != nil {
		var noMatch downloader.ErrNoMatch
		if errors.As(err, &noMatch) {
			// Not a transient failure: no Tidal album matched (e.g. garbled
			// ADM title). Mark it skipped rather than failed so `retry` does
			// not churn on it forever.
			p.logf("skipped (no match): %s — %s", item.Artist, item.Album)
			p.Queue.Skip(item.ReviewID, err)
			return downloader.Result{}, false
		}
		p.logf("download failed: %s — %s: %v", item.Artist, item.Album, err)
		p.Queue.Fail(item.ReviewID, err)
		return downloader.Result{}, false
	}
	if !res.Lossless {
		p.logf("note: only a lossy edition available for %s — %s; taking it as fallback", item.Artist, item.Album)
	}
	if err := p.Queue.SetFLACDir(item.ReviewID, res.AudioDir); err != nil {
		p.logf("set audio dir: %v", err)
	}
	if err := p.Queue.SetState(item.ReviewID, queue.StateConverting); err != nil {
		p.logf("set converting: %v", err)
	}
	return res, true
}

func (p *Pipeline) runConvert(ctx context.Context, item queue.Item, res downloader.Result) {
	r := adm.Release{ReviewID: item.ReviewID, Artist: item.Artist, Album: item.Album}
	audioDir := res.AudioDir
	p.logf("converting:  %s — %s", item.Artist, item.Album)

	out, err := p.Converter.Convert(ctx, r, audioDir, res.Lossless)
	if err != nil {
		p.logf("convert failed: %s — %s: %v", item.Artist, item.Album, err)
		p.Queue.Fail(item.ReviewID, err)
		return
	}
	// Clean up the now-empty scratch download dir (best effort).
	os.RemoveAll(audioDir)
	removeEmptyParents(audioDir, 2) // <review id>/<artist>/<album>

	if p.Organizer != nil {
		dest, err := p.Organizer.Organize(ctx, organize.Album{Dir: out, Tidal: res.Album, Tracks: res.Tracks})
		if errors.Is(err, filing.ErrNotMounted) || errors.Is(err, musicbrainz.ErrUnavailable) {
			// The library vanished, or MusicBrainz is down, mid-run: every
			// album after this one would fail the same way. Stop starting
			// new albums; this one stays converted in the output folder and
			// is retried later (`tidlr retry`).
			p.halt.Do(func() { p.logf("stopping: %v; pending albums stay queued", err) })
			p.halted.Store(true)
			p.Queue.Fail(item.ReviewID, err)
			return
		}
		if err != nil {
			// Held for review (or the organizer failed and left the album
			// where convert put it). Either way it is downloaded: record it
			// so it is not fetched again, and leave it for a person.
			var rev *organize.ReviewError
			if errors.As(err, &rev) {
				out = rev.Dir
			}
			p.logf("review:      %s — %s: %v", item.Artist, item.Album, err)
			if err := p.Queue.MarkDownloaded(item.ReviewID, item.Artist, item.Album, out); err != nil {
				p.logf("mark downloaded: %v", err)
			}
			p.Queue.Review(item.ReviewID, err)
			return
		}
		out = dest
	}

	// Record permanently so future scrapes skip it (unless --force).
	if err := p.Queue.MarkDownloaded(item.ReviewID, item.Artist, item.Album, out); err != nil {
		p.logf("mark downloaded: %v", err)
	}
	if err := p.Queue.SetState(item.ReviewID, queue.StateDone); err != nil {
		p.logf("set done: %v", err)
	}
	p.logf("done:        %s — %s -> %s", item.Artist, item.Album, out)
}

// removeEmptyParents removes up to n parent folders of dir, nearest first,
// stopping at the first that is not empty.
func removeEmptyParents(dir string, n int) {
	for ; n > 0; n-- {
		dir = filepath.Dir(dir)
		if os.Remove(dir) != nil {
			return
		}
	}
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log.Printf(format, args...)
	}
}
