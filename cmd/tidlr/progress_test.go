package main

import (
	"strings"
	"testing"

	"github.com/simonb/tidlr/internal/tidal"
)

// TestQueueProgressSerialAlbums drives two albums through the adapter the way
// the pipeline does, asserting the display is reset between them (no rows or
// counts leaking from the first album into the second).
func TestQueueProgressSerialAlbums(t *testing.T) {
	qp := &queueProgress{prog: newAlbumProgress(2), total: 5}

	onAlbum, onTrack, finish := qp.Begin("Artist A", "Album A")
	onAlbum(2)
	seg := onTrack(tidal.Track{Title: "A1"})
	seg(1, 1) // completes the track
	if got := qp.prog.done; got != 1 {
		t.Fatalf("after 1 track: done = %d, want 1", got)
	}
	finish()

	onAlbum, onTrack, finish = qp.Begin("Artist B", "Album B")
	if qp.prog.done != 0 || qp.prog.total != 0 {
		t.Errorf("state leaked into next album: done=%d total=%d, want 0/0", qp.prog.done, qp.prog.total)
	}
	for _, r := range qp.prog.rows {
		if r.active {
			t.Errorf("row still active at album start: %+v", r)
		}
	}
	onAlbum(3)
	seg = onTrack(tidal.Track{Title: "B1"})
	seg(1, 2) // partial
	if qp.prog.done != 0 {
		t.Errorf("partial track counted as done: %d", qp.prog.done)
	}
	finish()

	if qp.n != 2 {
		t.Errorf("album counter = %d, want 2", qp.n)
	}
}

// TestAlbumProgressWriteNonTTY checks the log writer passes bytes through
// unchanged when there is no terminal (the CI / redirected case).
func TestAlbumProgressWriteNonTTY(t *testing.T) {
	p := newAlbumProgress(2)
	p.tty = false
	line := "converting: X\n"
	n, err := p.Write([]byte(line))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(line) {
		t.Errorf("Write returned %d, want %d", n, len(line))
	}
}

// TestAlbumProgressMoreTracksThanRows covers the overflow slot: with more
// concurrent tracks than rows, the extra track must still get a working
// callback rather than being dropped.
func TestAlbumProgressMoreTracksThanRows(t *testing.T) {
	p := newAlbumProgress(1)
	p.tty = false
	p.onAlbumStart(3)
	s1 := p.onTrackStart(tidal.Track{Title: "one"})
	s2 := p.onTrackStart(tidal.Track{Title: "two"}) // no free slot
	if s1 == nil || s2 == nil {
		t.Fatal("onTrackStart returned nil callback")
	}
	s1(1, 1)
	s2(1, 1)
	if p.done != 2 {
		t.Errorf("done = %d, want 2", p.done)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "ab…" {
		t.Errorf("truncate = %q, want %q", got, "ab…")
	}
	if got := truncate("ab", 10); got != "ab" {
		t.Errorf("truncate = %q, want %q", got, "ab")
	}
	// Multi-byte titles must truncate by rune, not byte.
	if got := truncate("日本語です", 3); !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q, want ellipsis suffix", got)
	}
}

// TestAlbumProgressWriteRedrawsBelow asserts a log line written while the live
// display is up erases the region, prints the line, and redraws below it — so
// concurrent convert-worker logging never lands inside the region and gets
// overwritten by the next redraw's cursor rewind.
func TestAlbumProgressWriteRedrawsBelow(t *testing.T) {
	p := newAlbumProgress(2)
	p.tty = true
	p.onAlbumStart(4)
	seg := p.onTrackStart(tidal.Track{Title: "$20"})
	seg(2, 4)
	before := p.lastDraw
	if before == 0 {
		t.Fatal("expected a drawn region before writing")
	}
	if _, err := p.Write([]byte("converting: X\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The region must have been redrawn (not left cleared) so progress survives.
	if p.lastDraw != before {
		t.Errorf("lastDraw = %d after Write, want %d (region redrawn)", p.lastDraw, before)
	}
	// And the in-flight track's progress must be intact.
	if p.rows[0].done != 2 || !p.rows[0].active {
		t.Errorf("track state lost across Write: %+v", p.rows[0])
	}
}
