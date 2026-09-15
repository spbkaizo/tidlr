package queue

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/simonb/tidlr/internal/adm"
)

// The tests below exercise the SQLite driver's guarantees rather than the
// queue's business logic: persistence across reopen, exclusivity of ClaimPending
// under real concurrency, and WAL behaviour with a second connection. The rest
// of the suite opens a fresh database per test and never reopens or shares one,
// so a driver upgrade could regress any of this without turning a test red.

// TestPersistsAcrossReopen writes through one handle, closes it, and reads back
// through another. This is the on-disk format contract: an existing queue.db in
// a user's data dir must stay readable after the driver is upgraded.
func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")

	q, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := q.Enqueue(sample, false); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	it, err := q.ClaimPending(StateDownloading)
	if err != nil || it == nil {
		t.Fatalf("ClaimPending = %v, %v", it, err)
	}
	if err := q.MarkDownloaded(it.ReviewID, it.Artist, it.Album, "/out/dir"); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer q2.Close()

	counts, err := q2.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts[StatePending] != 1 {
		t.Errorf("pending after reopen = %d, want 1", counts[StatePending])
	}
	// The completed item must still be recorded, so a re-scrape skips it.
	n, err := q2.Enqueue(sample, false)
	if err != nil {
		t.Fatalf("Enqueue after reopen: %v", err)
	}
	if n != 0 {
		t.Errorf("re-enqueue added %d items, want 0 (both already known)", n)
	}
}

// TestClaimPendingIsExclusiveUnderConcurrency runs many goroutines claiming at
// once. The pipeline runs several download workers against one Queue, and
// correctness depends on no two ever receiving the same item — a guarantee that
// rests on transaction isolation plus SetMaxOpenConns(1), both driver behaviour.
func TestClaimPendingIsExclusiveUnderConcurrency(t *testing.T) {
	q := newTestQueue(t)

	const items = 50
	rel := make([]adm.Release, items)
	for i := range rel {
		rel[i] = adm.Release{ReviewID: i + 1, Artist: "A", Album: "Album", ReviewURL: "u"}
	}
	if _, err := q.Enqueue(rel, false); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	const workers = 8
	var (
		mu     sync.Mutex
		seen   = map[int]int{}
		errs   []error
		wg     sync.WaitGroup
		claims int
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, err := q.ClaimPending(StateDownloading)
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				if it == nil { // queue drained
					return
				}
				mu.Lock()
				seen[it.ReviewID]++
				claims++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("concurrent ClaimPending: %v", err)
	}
	if claims != items {
		t.Errorf("total claims = %d, want %d", claims, items)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("review_id %d claimed %d times, want exactly 1", id, n)
		}
	}
	if len(seen) != items {
		t.Errorf("distinct items claimed = %d, want %d", len(seen), items)
	}
}

// TestConcurrentReadersDuringWrite opens a second handle on the same file while
// the first is writing. Open sets journal_mode=WAL specifically so a reader is
// not blocked by a writer; this pins that the driver still honours it.
func TestConcurrentReadersDuringWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")

	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open writer: %v", err)
	}
	defer writer.Close()
	if _, err := writer.Enqueue(sample, false); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open reader: %v", err)
	}
	defer reader.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			rel := []adm.Release{{ReviewID: 100 + i, Artist: "W", Album: "Wr", ReviewURL: "u"}}
			if _, err := writer.Enqueue(rel, false); err != nil {
				t.Errorf("concurrent write: %v", err)
				return
			}
		}
	}()

	// Reads must succeed throughout, not block until the writer finishes.
	for range 20 {
		if _, err := reader.Counts(); err != nil {
			t.Errorf("concurrent read: %v", err)
			break
		}
	}
	wg.Wait()

	counts, err := reader.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if got := counts[StatePending]; got != 22 {
		t.Errorf("pending = %d, want 22 (2 seed + 20 concurrent)", got)
	}
}
