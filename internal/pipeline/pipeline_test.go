package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/downloader"
	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/organize"
	"github.com/simonb/tidlr/internal/queue"
)

// fakeDownloader "downloads" into a fresh folder; hook, if set, runs first
// with the call number (from 1) and may return an error.
type fakeDownloader struct {
	root  string
	calls atomic.Int32
	hook  func(n int, r adm.Release) error
}

func (d *fakeDownloader) Download(ctx context.Context, r adm.Release) (downloader.Result, error) {
	n := int(d.calls.Add(1))
	if d.hook != nil {
		if err := d.hook(n, r); err != nil {
			return downloader.Result{}, err
		}
	}
	dir := filepath.Join(d.root, "dl", fmt.Sprint(r.ReviewID), r.Artist, r.Album)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return downloader.Result{}, err
	}
	return downloader.Result{AudioDir: dir, Lossless: true}, nil
}

type fakeConverter struct{ root string }

func (c fakeConverter) Convert(ctx context.Context, r adm.Release, src string, lossless bool) (string, error) {
	out := filepath.Join(c.root, "out", r.Artist, r.Album)
	return out, os.MkdirAll(out, 0o755)
}

// fakeOrganizer files every album at "/library/<album>" unless organize says
// otherwise; have lists albums already in the library by title.
type fakeOrganizer struct {
	organize func(a organize.Album) (string, error)
	have     map[string]string
	calls    atomic.Int32
}

func (o *fakeOrganizer) Organize(ctx context.Context, a organize.Album) (string, error) {
	o.calls.Add(1)
	if o.organize != nil {
		return o.organize(a)
	}
	return "/library/" + filepath.Base(a.Dir), nil
}

func (o *fakeOrganizer) InLibrary(artist, title, year string) (string, bool) {
	dir, ok := o.have[title]
	return dir, ok
}

func setup(t *testing.T, albums ...string) (*Pipeline, *fakeDownloader) {
	t.Helper()
	root := t.TempDir()
	q, err := queue.Open(filepath.Join(root, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	var rel []adm.Release
	for i, a := range albums {
		rel = append(rel, adm.Release{ReviewID: 100 + i, Artist: "Artist", Album: a, ReviewURL: "u"})
	}
	if _, err := q.Enqueue(rel, false); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDownloader{root: root}
	return &Pipeline{Queue: q, Downloader: dl, Converter: fakeConverter{root}, DownloadWorkers: 1, ConvertWorkers: 1}, dl
}

func states(t *testing.T, q *queue.Queue) map[queue.State]int {
	t.Helper()
	c, err := q.Counts()
	if err != nil {
		t.Fatal(err)
	}
	for s, n := range c {
		if n == 0 {
			delete(c, s)
		}
	}
	return c
}

func wantStates(t *testing.T, q *queue.Queue, want map[queue.State]int) {
	t.Helper()
	got := states(t, q)
	if len(got) != len(want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	for s, n := range want {
		if got[s] != n {
			t.Fatalf("states = %v, want %v", got, want)
		}
	}
}

func TestRunFilesAlbums(t *testing.T) {
	p, dl := setup(t, "One", "Two")
	org := &fakeOrganizer{}
	p.Organizer = org
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantStates(t, p.Queue, map[queue.State]int{queue.StateDone: 2})
	if dl.calls.Load() != 2 || org.calls.Load() != 2 {
		t.Errorf("downloads %d, organizes %d; want 2 each", dl.calls.Load(), org.calls.Load())
	}
	if ok, _ := p.Queue.IsDownloaded(100); !ok {
		t.Error("filed album not recorded as downloaded")
	}
}

// An album the library already has is not downloaded, unless forced.
func TestRunSkipsAlbumsInLibrary(t *testing.T) {
	p, dl := setup(t, "Have", "New")
	p.Organizer = &fakeOrganizer{have: map[string]string{"Have": "/library/Have (Deluxe)"}}
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantStates(t, p.Queue, map[queue.State]int{queue.StateDone: 2})
	if dl.calls.Load() != 1 {
		t.Errorf("%d downloads, want 1 (the album not in the library)", dl.calls.Load())
	}

	p, dl = setup(t, "Have")
	p.Organizer = &fakeOrganizer{have: map[string]string{"Have": "/library/Have"}}
	p.Force = true
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dl.calls.Load() != 1 {
		t.Errorf("forced: %d downloads, want 1", dl.calls.Load())
	}
}

func TestRunOutcomes(t *testing.T) {
	p, dl := setup(t, "Garbled", "Unknown", "Broken")
	dl.hook = func(n int, r adm.Release) error {
		switch r.Album {
		case "Garbled":
			return downloader.ErrNoMatch{Query: r.Album}
		case "Broken":
			return errors.New("connection reset")
		}
		return nil
	}
	p.Organizer = &fakeOrganizer{organize: func(a organize.Album) (string, error) {
		return "", &organize.ReviewError{Reason: "no MusicBrainz release matches", Dir: "/review/Unknown"}
	}}
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantStates(t, p.Queue, map[queue.State]int{queue.StateSkipped: 1, queue.StateReview: 1, queue.StateFailed: 1})
	// Held for review still counts as downloaded, so it isn't fetched again.
	if ok, _ := p.Queue.IsDownloaded(101); !ok {
		t.Error("album held for review not recorded as downloaded")
	}
}

// A MusicBrainz outage fails the album for a retry and stops new albums
// starting; the one already downloading finishes, the rest stay queued.
func TestRunStopsOnMusicBrainzOutage(t *testing.T) {
	p, dl := setup(t, "A", "B", "C")
	dl.hook = func(n int, r adm.Release) error {
		if n == 2 {
			// Hold the second download until the first album has failed and
			// the pipeline has stopped, so the third is never claimed.
			deadline := time.Now().Add(5 * time.Second)
			for !p.halted.Load() && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
		}
		return nil
	}
	p.Organizer = &fakeOrganizer{organize: func(a organize.Album) (string, error) {
		return "", fmt.Errorf("identify: %w", musicbrainz.ErrUnavailable)
	}}
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dl.calls.Load() != 2 {
		t.Errorf("%d downloads, want 2", dl.calls.Load())
	}
	wantStates(t, p.Queue, map[queue.State]int{queue.StateFailed: 2, queue.StatePending: 1})
	if ok, _ := p.Queue.IsDownloaded(100); ok {
		t.Error("an album failed by the outage was recorded as downloaded")
	}
}

func TestRemoveEmptyParents(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "8559", "Mount Eerie", "Night Palace")
	other := filepath.Join(root, "9000", "Geese", "Getting Killed")
	for _, d := range []string{album, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.RemoveAll(album)
	removeEmptyParents(album, 2)
	if _, err := os.Stat(filepath.Join(root, "8559")); !os.IsNotExist(err) {
		t.Errorf("empty scratch folders left: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another album's folder went: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("scratch root went: %v", err)
	}
}
