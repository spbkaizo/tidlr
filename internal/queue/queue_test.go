package queue

import (
	"path/filepath"
	"testing"

	"github.com/simonb/tidlr/internal/adm"
)

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	q, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

var sample = []adm.Release{
	{ReviewID: 1, Artist: "A", Album: "One", ReviewURL: "u1"},
	{ReviewID: 2, Artist: "B", Album: "Two", ReviewURL: "u2"},
}

func TestEnqueueDedupe(t *testing.T) {
	q := newTestQueue(t)

	n, err := q.Enqueue(sample, false)
	if err != nil || n != 2 {
		t.Fatalf("first enqueue = %d, %v; want 2", n, err)
	}
	// Re-enqueuing the same items plus one new one adds only the new one.
	n, err = q.Enqueue(append(sample, adm.Release{ReviewID: 3, Artist: "C", Album: "Three", ReviewURL: "u3"}), false)
	if err != nil || n != 1 {
		t.Fatalf("second enqueue = %d, %v; want 1", n, err)
	}

	counts, _ := q.Counts()
	if counts[StatePending] != 3 {
		t.Fatalf("pending = %d, want 3", counts[StatePending])
	}
}

func TestClaimIsExclusiveAndOrdered(t *testing.T) {
	q := newTestQueue(t)
	if _, err := q.Enqueue(sample, false); err != nil {
		t.Fatal(err)
	}

	first, err := q.ClaimPending(StateDownloading)
	if err != nil || first == nil {
		t.Fatalf("claim1 = %v, %v", first, err)
	}
	if first.ReviewID != 1 { // oldest (created_at) first; id 1 inserted first
		t.Errorf("claimed id %d, want 1", first.ReviewID)
	}
	if first.State != StateDownloading || first.Attempts != 1 {
		t.Errorf("claimed state=%s attempts=%d", first.State, first.Attempts)
	}

	second, _ := q.ClaimPending(StateDownloading)
	if second == nil || second.ReviewID == first.ReviewID {
		t.Fatalf("second claim must be a different item, got %+v", second)
	}

	// No pending left.
	third, _ := q.ClaimPending(StateDownloading)
	if third != nil {
		t.Errorf("expected nil claim, got %+v", third)
	}
}

func TestLifecycle(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample[:1], false)

	it, _ := q.ClaimPending(StateDownloading)
	if err := q.SetFLACDir(it.ReviewID, "/scratch/x"); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(it.ReviewID, StateConverting); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(it.ReviewID, StateDone); err != nil {
		t.Fatal(err)
	}
	counts, _ := q.Counts()
	if counts[StateDone] != 1 {
		t.Fatalf("done = %d, want 1", counts[StateDone])
	}
}

func TestFailRecordsError(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample[:1], false)
	it, _ := q.ClaimPending(StateDownloading)
	if err := q.Fail(it.ReviewID, errTest); err != nil {
		t.Fatal(err)
	}
	counts, _ := q.Counts()
	if counts[StateFailed] != 1 {
		t.Fatalf("failed = %d, want 1", counts[StateFailed])
	}
}

func TestRequeueFailed(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample, false)
	it, _ := q.ClaimPending(StateDownloading)
	q.Fail(it.ReviewID, errTest)

	n, err := q.RequeueFailed()
	if err != nil || n != 1 {
		t.Fatalf("RequeueFailed = %d, %v; want 1", n, err)
	}
	counts, _ := q.Counts()
	if counts[StateFailed] != 0 || counts[StatePending] != 2 {
		t.Fatalf("after requeue: pending=%d failed=%d, want 2/0",
			counts[StatePending], counts[StateFailed])
	}
}

func TestSkipRecordsError(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample[:1], false)
	it, _ := q.ClaimPending(StateDownloading)
	if err := q.Skip(it.ReviewID, errTest); err != nil {
		t.Fatal(err)
	}
	counts, _ := q.Counts()
	if counts[StateSkipped] != 1 {
		t.Fatalf("skipped = %d, want 1", counts[StateSkipped])
	}
	if counts[StateFailed] != 0 {
		t.Fatalf("failed = %d, want 0 (skip must not count as failed)", counts[StateFailed])
	}
}

// A skipped item is permanent: `retry` must leave it alone, or a genuinely
// unmatchable album churns on every run forever.
func TestRequeueFailedLeavesSkipped(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample, false)

	failed, _ := q.ClaimPending(StateDownloading)
	q.Fail(failed.ReviewID, errTest)
	skipped, _ := q.ClaimPending(StateDownloading)
	q.Skip(skipped.ReviewID, errTest)

	n, err := q.RequeueFailed()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RequeueFailed = %d, want 1 (the failed item only)", n)
	}

	counts, _ := q.Counts()
	if counts[StateSkipped] != 1 {
		t.Errorf("skipped = %d, want 1 (must survive requeue)", counts[StateSkipped])
	}
	if counts[StatePending] != 1 {
		t.Errorf("pending = %d, want 1", counts[StatePending])
	}

	// Repeated retries must never resurrect it.
	for i := 0; i < 3; i++ {
		q.RequeueFailed()
	}
	counts, _ = q.Counts()
	if counts[StateSkipped] != 1 {
		t.Errorf("after repeated retries: skipped = %d, want 1", counts[StateSkipped])
	}
}

func TestRequeueStale(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample, false)
	// Simulate a crash: claim both, leave them mid-flight.
	q.ClaimPending(StateDownloading)
	it2, _ := q.ClaimPending(StateDownloading)
	q.SetState(it2.ReviewID, StateConverting)

	n, err := q.RequeueStale()
	if err != nil || n != 2 {
		t.Fatalf("RequeueStale = %d, %v; want 2", n, err)
	}
	counts, _ := q.Counts()
	if counts[StatePending] != 2 {
		t.Fatalf("after stale requeue pending=%d, want 2", counts[StatePending])
	}
}

func TestEnqueueSkipsDownloadedUnlessForce(t *testing.T) {
	q := newTestQueue(t)

	// Record id 1 as already downloaded.
	if err := q.MarkDownloaded(1, "A", "One", "/music/A/One"); err != nil {
		t.Fatal(err)
	}
	if have, _ := q.IsDownloaded(1); !have {
		t.Fatal("id 1 should be marked downloaded")
	}

	// Without force, id 1 is skipped; only id 2 enqueues.
	n, err := q.Enqueue(sample, false)
	if err != nil || n != 1 {
		t.Fatalf("enqueue non-force = %d, %v; want 1", n, err)
	}
	counts, _ := q.Counts()
	if counts[StatePending] != 1 {
		t.Fatalf("pending = %d, want 1 (id 1 skipped)", counts[StatePending])
	}

	// With force, both are (re)enqueued: id 1 newly forced in, id 2 reset.
	n, err = q.Enqueue(sample, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("enqueue force added = %d, want 2", n)
	}
	counts, _ = q.Counts()
	if counts[StatePending] != 2 {
		t.Fatalf("pending after force = %d, want 2", counts[StatePending])
	}
}

func TestForceResetsCompletedItem(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sample[:1], false)
	it, _ := q.ClaimPending(StateDownloading)
	q.MarkDownloaded(it.ReviewID, "A", "One", "/music/A/One")
	q.SetState(it.ReviewID, StateDone)

	// Force re-enqueue should flip the done row back to pending.
	n, err := q.Enqueue(sample[:1], true)
	if err != nil || n != 1 {
		t.Fatalf("force re-enqueue = %d, %v; want 1", n, err)
	}
	counts, _ := q.Counts()
	if counts[StatePending] != 1 || counts[StateDone] != 0 {
		t.Fatalf("after force: pending=%d done=%d, want 1/0",
			counts[StatePending], counts[StateDone])
	}
}

var errTest = &testError{"boom"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }

// TestTidalDownloads covers the --album library record: an album is not
// "downloaded" until marked, lookups return what was stored, and marking is
// idempotent (a --force re-download refreshes rather than duplicating).
func TestTidalDownloads(t *testing.T) {
	q := newTestQueue(t)

	const albumID = 560221929
	if have, err := q.IsTidalDownloaded(albumID); err != nil || have {
		t.Fatalf("IsTidalDownloaded before mark = %v, %v; want false, nil", have, err)
	}
	if _, _, _, ok, err := q.TidalDownload(albumID); err != nil || ok {
		t.Fatalf("TidalDownload before mark = ok %v, err %v; want false, nil", ok, err)
	}

	if err := q.MarkTidalDownloaded(albumID, "Carly Rae Jepsen", "Day and Night", "/music/CRJ/Day and Night"); err != nil {
		t.Fatalf("MarkTidalDownloaded: %v", err)
	}
	have, err := q.IsTidalDownloaded(albumID)
	if err != nil || !have {
		t.Fatalf("IsTidalDownloaded after mark = %v, %v; want true, nil", have, err)
	}
	artist, album, dir, ok, err := q.TidalDownload(albumID)
	if err != nil || !ok {
		t.Fatalf("TidalDownload after mark = ok %v, err %v", ok, err)
	}
	if artist != "Carly Rae Jepsen" || album != "Day and Night" || dir != "/music/CRJ/Day and Night" {
		t.Errorf("TidalDownload = %q, %q, %q", artist, album, dir)
	}

	// Re-marking (the --force path) updates in place.
	if err := q.MarkTidalDownloaded(albumID, "Carly Rae Jepsen", "Day and Night", "/music/new"); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if _, _, dir, _, _ := q.TidalDownload(albumID); dir != "/music/new" {
		t.Errorf("after re-mark dir = %q, want /music/new", dir)
	}

	// A different album id is unaffected.
	if have, _ := q.IsTidalDownloaded(535577394); have {
		t.Error("unrelated album id reported as downloaded")
	}
}

// TestTidalDownloadsSeparateFromADM guards the id-space split: an ADM review id
// and a Tidal album id that happen to be equal must not alias each other.
func TestTidalDownloadsSeparateFromADM(t *testing.T) {
	q := newTestQueue(t)

	const id = 14619
	if err := q.MarkDownloaded(id, "Aldous Harding", "Train On The Island", "/music/a"); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	// The same number as a Tidal album id must still be "not downloaded".
	if have, err := q.IsTidalDownloaded(id); err != nil || have {
		t.Errorf("ADM review id leaked into Tidal library: have=%v err=%v", have, err)
	}
}
