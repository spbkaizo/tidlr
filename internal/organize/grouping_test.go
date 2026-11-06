package organize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecideGrouping(t *testing.T) {
	lib := t.TempDir()
	dir := func(rel string) string {
		p := filepath.Join(lib, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	selfTitled := dir("Archive Albums/Electronic/Parcels - 2018 - Parcels")
	live := dir("Archive Albums/Disco/Parcels - 2020 - Live Vol. 1")
	dir("Archive Albums/Disco/Parcels - 2021 - Day_Night")
	dir("Archive Albums/Unknown/Parcels - 2019 - Overnight") // Unknown never counts
	joy := dir("Archive Albums/Punk Rock/IDLES - 2018 - Joy as an Act of Resistance_")
	dir("Archive Albums/Punk/IDLES - 2021 - Crawler")
	dir("Archive Albums/Indie/Various Artists - 2010 - Comp")
	dir("Archive Albums/Indie/Various Artists - 2011 - Comp 2")

	o := &Organizer{LibraryDir: lib, Merge: map[string]string{"Punk Rock": "Punk"}, KeepArtistTogether: true}
	o.loadArtists()

	cases := []struct {
		name                       string
		computed, albumDir, folder string
		current                    string
		want, reason               string
	}{
		{"artist's majority folder wins", "Electronic", selfTitled, "", "Electronic", "Disco", "artist"},
		// Day_Night is excluded from its own count, leaving Disco 1 vs
		// Electronic 1: the computed genre breaks the tie.
		{"tie goes to the computed genre", "Disco", filepath.Join(lib, "Archive Albums/Disco/Parcels - 2021 - Day_Night"), "", "Disco", "Disco", ""},
		{"tie with neither: current folder", "House", live, "", "Disco", "Disco", "artist"},
		{"new album with no genre joins the artist", "", "", "Parcels - 2024 - New", "", "Disco", "artist"},
		{"merged genre, and merged folder counts", "Punk Rock", joy, "", "Punk Rock", "Punk", ""},
		{"Various Artists are not kept together", "Electronic", "", "Various Artists - 2012 - Comp 3", "", "Electronic", ""},
		{"artist with no other albums", "Folk", "", "Nobody - 2020 - Debut", "", "Folk", ""},
	}
	for _, c := range cases {
		name := c.folder
		if name == "" {
			name = filepath.Base(c.albumDir)
		}
		got, reason := o.decideGrouping(c.computed, "Archive Albums", name, c.albumDir, c.current)
		if got != c.want || reason != c.reason {
			t.Errorf("%s: got %q (%q), want %q (%q)", c.name, got, reason, c.want, c.reason)
		}
	}

	o.KeepArtistTogether = false
	if got, _ := o.decideGrouping("Punk Rock", "Archive Albums", "IDLES - 2018 - Joy", joy, "Punk Rock"); got != "Punk" {
		t.Errorf("merge without the artist rule: got %q, want Punk", got)
	}
}
