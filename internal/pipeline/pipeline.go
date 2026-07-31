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
	"sync"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/queue"
)

// Pipeline processes queued albums.
type Pipeline struct {
	Queue      *queue.Queue
	Downloader downloader.Downloader
	Converter  *convert.Converter

	DownloadWorkers int
	ConvertWorkers  int

	Log *log.Logger
}

// Run drains all currently-pending items and returns when the queue has no more
// pending work (or ctx is cancelled). Downloaded albums are handed to the
// converter via an in-process channel so conversion overlaps with downloading.
func (p *Pipeline) Run(ctx context.Context) error {
	type job struct {
		item     queue.Item
		audioDir string
		lossless bool
	}
	convertCh := make(chan job, p.ConvertWorkers*2)

	// Convert workers.
	var convWG sync.WaitGroup
	for i := 0; i < max(p.ConvertWorkers, 1); i++ {
		convWG.Add(1)
		go func() {
			defer convWG.Done()
			for j := range convertCh {
				p.runConvert(ctx, j.item, j.audioDir, j.lossless)
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
				if ctx.Err() != nil {
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
				if dir, lossless, ok := p.runDownload(ctx, *item); ok {
					convertCh <- job{item: *item, audioDir: dir, lossless: lossless}
				}
			}
		}()
	}

	dlWG.Wait()
	close(convertCh)
	convWG.Wait()
	return ctx.Err()
}

func (p *Pipeline) runDownload(ctx context.Context, item queue.Item) (string, bool, bool) {
	r := adm.Release{ReviewID: item.ReviewID, Artist: item.Artist, Album: item.Album, ReviewURL: item.ReviewURL}
	p.logf("downloading: %s — %s", item.Artist, item.Album)

	res, err := p.Downloader.Download(ctx, r)
	if err != nil {
		var noMatch downloader.ErrNoMatch
		if errors.As(err, &noMatch) {
			// Not a transient failure: no Tidal album matched (e.g. garbled
			// ADM title). Mark it skipped rather than failed so `retry` does
			// not churn on it forever.
			p.logf("skipped (no match): %s — %s", item.Artist, item.Album)
			p.Queue.Skip(item.ReviewID, err)
			return "", false, false
		}
		p.logf("download failed: %s — %s: %v", item.Artist, item.Album, err)
		p.Queue.Fail(item.ReviewID, err)
		return "", false, false
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
	return res.AudioDir, res.Lossless, true
}

func (p *Pipeline) runConvert(ctx context.Context, item queue.Item, audioDir string, lossless bool) {
	r := adm.Release{ReviewID: item.ReviewID, Artist: item.Artist, Album: item.Album}
	p.logf("converting:  %s — %s", item.Artist, item.Album)

	out, err := p.Converter.Convert(ctx, r, audioDir, lossless)
	if err != nil {
		p.logf("convert failed: %s — %s: %v", item.Artist, item.Album, err)
		p.Queue.Fail(item.ReviewID, err)
		return
	}
	// Clean up the now-empty scratch download dir (best effort).
	os.RemoveAll(audioDir)

	// Record permanently so future scrapes skip it (unless --force).
	if err := p.Queue.MarkDownloaded(item.ReviewID, item.Artist, item.Album, out); err != nil {
		p.logf("mark downloaded: %v", err)
	}
	if err := p.Queue.SetState(item.ReviewID, queue.StateDone); err != nil {
		p.logf("set done: %v", err)
	}
	p.logf("done:        %s — %s -> %s", item.Artist, item.Album, out)
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log.Printf(format, args...)
	}
}
