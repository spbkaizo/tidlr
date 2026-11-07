package organize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlbumKey(t *testing.T) {
	same := [][4]string{
		{"Nick Cave & The Bad Seeds", "Ghosteen", "Nick Cave and the Bad Seeds", "Ghosteen"},
		{"The KLF", "Chill Out", "KLF", "Chill Out"},
		{"Kendrick Lamar", "DAMN.", "Kendrick Lamar", "DAMN_"},
		{"Fiona Apple", "Fetch the Bolt Cutters", "Fiona Apple", "Fetch The Bolt Cutters (Deluxe Edition)"},
		{"Tricky", "PROOF", "Tricky", "PROOF [Explicit]"},
		{"José González", "Veneer", "José González", "Veneer (20th Anniversary Deluxe Edition)"},
		{"Mr. Scruff", "Keep It Unreal", "Mr. Scruff", "Keep It Unreal (10th anniversary analogue remaster edition)"},
		{"Public Enemy", "What You Gonna Do When the Grid Goes Down?", "Public Enemy", "What You Gonna Do When the Grid Goes Down~"},
	}
	for _, c := range same {
		if a, b := albumKey(c[0], c[1]), albumKey(c[2], c[3]); a != b {
			t.Errorf("%q / %q and %q / %q should match: %q vs %q", c[0], c[1], c[2], c[3], a, b)
		}
	}
	differ := [][4]string{
		{"Pink Floyd", "Animals", "Pink Floyd", "Meddle"},
		{"Kraftwerk", "Computer World", "Kraftwerk", "Computerwelt"},
		{"Nicki Minaj", "Pink Friday", "Nicki Minaj", "Pink Friday 2"},
		// From the real library: other albums, not other editions.
		{"George FitzGerald", "All That Must Be", "George FitzGerald", "All That Must Be (Remixes)"},
		{"SAULT", "UNTITLED (Black Is)", "SAULT", "UNTITLED (Rise)"},
		{"New Order", "(The Best of) New Order", "New Order", "(The Rest of) New Order"},
		{"Agnes Obel", "Myopia", "Agnes Obel", "Myopia (Instrumentals)"},
		{"Taylor Swift", "1989", "Taylor Swift", "1989 (Taylor's version) (deluxe)"},
	}
	for _, c := range differ {
		if albumKey(c[0], c[1]) == albumKey(c[2], c[3]) {
			t.Errorf("%q / %q and %q / %q should not match", c[0], c[1], c[2], c[3])
		}
	}
}

func TestLibraryIndexBothLayouts(t *testing.T) {
	lib := t.TempDir()
	for _, d := range []string{
		"Archive Albums/Rock/Nick Cave and the Bad Seeds - 2019 - Ghosteen",
		"Geese/2025 - Getting Killed",
		"Archive Albums/New Wave/Duran Duran - 1981 - Duran Duran",
		"Archive Albums/Rock", // a genre folder alone is not an album
	} {
		if err := os.MkdirAll(filepath.Join(lib, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Organizer{LibraryDir: lib}
	if dir, ok := o.InLibrary("Nick Cave & The Bad Seeds", "Ghosteen", "2019"); !ok || !strings.HasSuffix(dir, "Ghosteen") {
		t.Errorf("genre layout: %q, %v", dir, ok)
	}
	if _, ok := o.InLibrary("Geese", "Getting Killed (Deluxe)", ""); !ok {
		t.Error("artist layout album not found")
	}
	if _, ok := o.InLibrary("Duran Duran", "Duran Duran", "1993"); ok {
		t.Error("the 1993 self-titled album matched the 1981 one")
	}
	if _, ok := o.InLibrary("Duran Duran", "Duran Duran", "1981"); !ok {
		t.Error("same album, same year, not found")
	}
	if _, ok := o.InLibrary("Geese", "3D Country", ""); ok {
		t.Error("found an album that isn't there")
	}
}

// An album already filed under a different name is held, not filed twice.
func TestOrganizeHoldsLooseDuplicate(t *testing.T) {
	a := downloaded(t, "440082667") // Geese — Getting Killed
	o := organizer(t)
	have := filepath.Join(o.LibraryDir, "Archive Albums", "Rock", "Geese - 2025 - Getting Killed (Deluxe)")
	if err := os.MkdirAll(have, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := o.Organize(context.Background(), a)
	var rev *ReviewError
	if !errors.As(err, &rev) || !strings.Contains(rev.Reason, have) {
		t.Fatalf("err = %v, want held for review as already at %s", err, have)
	}
}
