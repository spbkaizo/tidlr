package organize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileAs files an untagged album, as held for review, by a MusicBrainz
// release id the user picked.
func TestFileAs(t *testing.T) {
	a := downloaded(t, "440082667") // Geese — Getting Killed
	o := organizer(t)
	dest, err := o.FileAs(context.Background(), a.Dir, "2b637090-036e-4f86-8183-e1be856e1421")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(o.LibraryDir, "Archive Albums", "Psychedelic Rock", "Geese - 2025 - Getting Killed"); dest != want {
		t.Errorf("filed at %s, want %s", dest, want)
	}
	if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
		t.Errorf("source folder left behind: %v", err)
	}
	tags, err := o.Tagger.Read(context.Background(), filepath.Join(dest, "01 - Trinidad.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tags["MusicBrainz Album Id"]; got != "2b637090-036e-4f86-8183-e1be856e1421" {
		t.Errorf("MusicBrainz Album Id = %q", got)
	}
}

func TestFileAsWrongTrackCount(t *testing.T) {
	a := downloaded(t, "440082667")
	files, _ := filepath.Glob(filepath.Join(a.Dir, "*.m4a"))
	os.Remove(files[0])
	o := organizer(t)
	_, err := o.FileAs(context.Background(), a.Dir, "2b637090-036e-4f86-8183-e1be856e1421")
	if err == nil || !strings.Contains(err.Error(), "tracks") {
		t.Fatalf("err = %v, want a track count mismatch", err)
	}
	if left, _ := filepath.Glob(filepath.Join(a.Dir, "*.m4a")); len(left) != len(files)-1 {
		t.Errorf("album moved or changed: %d files left", len(left))
	}
}

func TestLeadingInt(t *testing.T) {
	for in, want := range map[string]int{"3 of 12": 3, "7": 7, "": 0, "x": 0} {
		if got := leadingInt(in); got != want {
			t.Errorf("leadingInt(%q) = %d, want %d", in, got, want)
		}
	}
}
