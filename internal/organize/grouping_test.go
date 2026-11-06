package organize

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/simonb/tidlr/internal/genre"
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

	jungle := dir("Archive Albums/Jungle/Goldie - 1995 - Timeless")
	dir("Archive Albums/Electronic/Goldie - 1998 - Saturnz Return")
	dir("Archive Albums/Electronic/Goldie - 2017 - The Journey Man")

	o := &Organizer{LibraryDir: lib, Merge: map[string]string{"Punk Rock": "Punk"}, KeepArtistTogether: true}
	o.loadArtists()

	cases := []struct {
		name                       string
		computed, albumDir, folder string
		current                    string
		want, reason               string
		genres                     []string
	}{
		{"artist's folder wins when the album is tagged that genre too", "Electronic", selfTitled, "", "Electronic", "Disco", "artist", []string{"Disco", "Funk"}},
		{"artist's folder ignored when the album isn't that genre", "Electronic", selfTitled, "", "Electronic", "Electronic", "", []string{"Synthpop"}},
		{"niche album stays put despite the artist's majority", "Jungle", jungle, "", "Jungle", "Jungle", "", []string{"Drum and Bass", "Breakbeat"}},
		{"...unless the album itself is tagged with the majority genre", "Jungle", jungle, "", "Jungle", "Electronic", "artist", []string{"Electronic"}},
		// Day_Night is excluded from its own count, leaving Disco 1 vs
		// Electronic 1: the computed genre breaks the tie.
		{"tie goes to the computed genre", "Disco", filepath.Join(lib, "Archive Albums/Disco/Parcels - 2021 - Day_Night"), "", "Disco", "Disco", "", []string{"Electronic"}},
		{"tie with neither: current folder", "House", live, "", "Disco", "Disco", "artist", []string{"Disco", "Electronic"}},
		{"new album with no genre joins the artist", "", "", "Parcels - 2024 - New", "", "Disco", "artist", nil},
		{"merged genre, and merged folder counts", "Punk Rock", joy, "", "Punk Rock", "Punk", "", nil},
		{"Various Artists are not kept together", "Electronic", "", "Various Artists - 2012 - Comp 3", "", "Electronic", "", []string{"Indie"}},
		{"artist with no other albums", "Folk", "", "Nobody - 2020 - Debut", "", "Folk", "", nil},
	}
	for _, c := range cases {
		name := c.folder
		if name == "" {
			name = filepath.Base(c.albumDir)
		}
		got, reason := o.decideGrouping(genre.Result{Grouping: c.computed, Genres: c.genres}, "Archive Albums", name, c.albumDir, c.current)
		if got != c.want || reason != c.reason {
			t.Errorf("%s: got %q (%q), want %q (%q)", c.name, got, reason, c.want, c.reason)
		}
	}

	o.KeepArtistTogether = false
	if got, _ := o.decideGrouping(genre.Result{Grouping: "Punk Rock"}, "Archive Albums", "IDLES - 2018 - Joy", joy, "Punk Rock"); got != "Punk" {
		t.Errorf("merge without the artist rule: got %q, want Punk", got)
	}
}
