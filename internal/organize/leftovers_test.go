package organize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbLeftovers(t *testing.T) {
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	album := filepath.Join(root, "Electronic", "A - 2019 - X")
	write(filepath.Join(album, "01 - a.m4a"), "audio")
	write(filepath.Join(album, "cover.jpg"), "new cover")

	// A shell holding a rip log joins the album and is removed.
	shell := filepath.Join(root, "Trip-Hop", "A - 2019 - X")
	write(filepath.Join(shell, "auCDtect.log"), "log")
	if err := absorbLeftovers(shell, album); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shell); !os.IsNotExist(err) {
		t.Errorf("shell not removed: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(album, "auCDtect.log")); string(b) != "log" {
		t.Errorf("log not moved into the album")
	}

	// A leftover named like an album file is kept beside it.
	write(filepath.Join(shell, "cover.jpg"), "old cover")
	if err := absorbLeftovers(shell, album); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(album, "cover.jpg")); string(b) != "new cover" {
		t.Errorf("album cover replaced")
	}
	if b, _ := os.ReadFile(filepath.Join(album, "cover (leftover).jpg")); string(b) != "old cover" {
		t.Errorf("leftover cover not kept")
	}

	// A folder with audio is a real copy.
	other := filepath.Join(root, "Jazz", "A - 2019 - X")
	write(filepath.Join(other, "01 - a.m4a"), "audio")
	if err := absorbLeftovers(other, album); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("real copy: err = %v", err)
	}
}

// Album folders can hold "[...]", which filepath.Glob would read as a pattern.
func TestFilesWithExtBrackets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Tricky - 2017 - PROOF [Explicit]")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"02 - b.m4a", "01 - a.M4A", "cover.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := filesWithExt(dir, ".m4a")
	if len(got) != 2 || filepath.Base(got[0]) != "01 - a.M4A" {
		t.Errorf("filesWithExt = %v", got)
	}
}

// Interrupted tag writes are not tracks, and are swept before tagging.
func TestTagTemps(t *testing.T) {
	dir := t.TempDir()
	names := []string{"01 - Song.m4a", "01 - Song-temp-80603.m4a", ".tidlr-retag-01 - Song.m4a", "02 - Attempt-1.m4a"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := filesWithExt(dir, ".m4a")
	if len(got) != 2 || filepath.Base(got[0]) != "01 - Song.m4a" || filepath.Base(got[1]) != "02 - Attempt-1.m4a" {
		t.Errorf("tracks = %v", got)
	}
	(&Organizer{}).sweepTagTemps(dir)
	left, _ := os.ReadDir(dir)
	if len(left) != 2 {
		t.Errorf("after sweep: %v", left)
	}
}
