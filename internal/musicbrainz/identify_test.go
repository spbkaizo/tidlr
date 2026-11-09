package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/simonb/tidlr/internal/musicbrainz/mbtest"
	"github.com/simonb/tidlr/internal/tidal"
)

func replayClient(t *testing.T) *Client {
	c := New("tidlr", "test")
	c.HTTP = &http.Client{Transport: mbtest.Replay{Dir: filepath.Join("testdata", "http"), T: t}}
	c.Interval = 1 // no need to rate limit recordings
	return c
}

// wantFromTidal builds a Want from a recorded Tidal album and track listing.
func wantFromTidal(t *testing.T, id string) Want {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "tidal", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Album tidal.Album      `json:"album"`
		Items tidal.AlbumItems `json:"items"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	want := Want{Artist: fx.Album.ArtistName(), Title: fx.Album.Title, Barcode: fx.Album.UPC}
	for _, it := range fx.Items.Items {
		want.Tracks = append(want.Tracks, WantTrack{Seconds: it.Item.Duration, ISRC: it.Item.ISRC})
	}
	return want
}

func TestIdentify(t *testing.T) {
	// Albums downloaded from Tidal, with the release Picard-equivalent
	// matching should land on. Release ids checked by hand on musicbrainz.org.
	cases := []struct {
		tidalID string
		method  string
		release string // MusicBrainz release id
		discs   int
	}{
		{"440082667", "barcode", "2b637090-036e-4f86-8183-e1be856e1421", 1}, // Geese — Getting Killed
		{"111388551", "barcode", "7b868580-7bcf-4bcb-a8ce-c0ee11973f35", 3}, // Joanna Newsom — Have One On Me
		{"20556792", "barcode", "712928ae-a147-4c62-b597-12e14accaba3", 2},  // Kendrick Lamar — good kid, m.A.A.d city (Deluxe)
		{"506480825", "barcode", "fa198feb-af0b-4d07-a65f-3f53fb8bff46", 1}, // underscores — U
		{"453064280", "barcode", "e3519ad5-311a-482a-8ee7-15c8a53aef52", 1}, // Dijon — Baby
		{"28384190", "barcode", "34a8b289-c35b-4a93-ab76-ae26ce713c8b", 2},  // Swans — To Be Kind
		{"409386860", "barcode", "c9e6b642-a6d9-4b22-8ec1-16c47b6143fc", 1}, // Bad Bunny — DeBÍ TiRAR MáS FOToS
		// Tidal's barcode is not on MusicBrainz; found by name and track list.
		{"486357700", "search", "92d44caf-6c0d-44a7-a841-ec16cbce73e1", 1}, // Mbongwana Star — From Kinshasa
	}
	c := replayClient(t)
	for _, tc := range cases {
		want := wantFromTidal(t, tc.tidalID)
		m, err := c.Identify(context.Background(), want, DefaultPrefs)
		if err != nil {
			t.Errorf("%s %s — %s: %v", tc.tidalID, want.Artist, want.Title, err)
			continue
		}
		r := m.Release
		t.Logf("%s %-28s %-7s %s %s %-10s %q group=%s", tc.tidalID, want.Artist, m.Method, r.ID, r.Country, r.Date, r.Title, r.ReleaseGroup.ID)
		if r.ID != tc.release {
			t.Errorf("%s: release %s, want %s", want.Title, r.ID, tc.release)
		}
		if m.Method != tc.method {
			t.Errorf("%s: method %s, want %s", want.Title, m.Method, tc.method)
		}
		if len(r.Media) != tc.discs {
			t.Errorf("%s: %d media, want %d", want.Title, len(r.Media), tc.discs)
		}
		if !Fits(r, want) {
			t.Errorf("%s: chosen release does not fit", want.Title)
		}
	}
}

func TestFits(t *testing.T) {
	r := Release{Media: []Medium{{Tracks: []Track{{Length: 180_000}, {Length: 0, Recording: Recording{Length: 200_400}}}}}}
	if !Fits(r, Want{Tracks: []WantTrack{{Seconds: 181}, {Seconds: 200}}}) {
		t.Error("lengths within tolerance should fit")
	}
	if Fits(r, Want{Tracks: []WantTrack{{Seconds: 190}, {Seconds: 200}}}) {
		t.Error("10s difference should not fit")
	}
	if Fits(r, Want{Tracks: []WantTrack{{Seconds: 180}}}) {
		t.Error("different track count should not fit")
	}

	// From real albums: one track a few seconds out on another master.
	album := func(lengths ...int) Release {
		var tr []Track
		for _, l := range lengths {
			tr = append(tr, Track{Length: l * 1000})
		}
		return Release{Media: []Medium{{Tracks: tr}}}
	}
	want := func(secs ...int) Want {
		var w Want
		for _, s := range secs {
			w.Tracks = append(w.Tracks, WantTrack{Seconds: s})
		}
		return w
	}
	creeper := want(46, 159, 177, 207, 18, 229, 243, 208, 202, 205, 22, 214, 7, 215, 255)
	if !Fits(album(46, 159, 176, 206, 18, 224, 243, 208, 202, 205, 22, 214, 7, 215, 255), creeper) {
		t.Error("one track 5s out on a 15-track album should fit")
	}
	if Fits(album(46, 159, 176, 206, 18, 224, 243, 208, 197, 205, 22, 214, 7, 215, 255), creeper) {
		t.Error("two tracks 5s out on a 15-track album should not fit")
	}
	if Fits(album(46, 159, 177, 207, 18, 229, 243, 208, 202, 205, 22, 214, 7, 215, 270), creeper) {
		t.Error("a track 15s out should not fit")
	}
	// Same track count, different album: close per track, far on average.
	if Fits(album(48, 157, 179, 205, 20, 227, 245, 206, 204, 203, 24, 212, 9, 213, 257), creeper) {
		t.Error("tracks alternately 2s long and short should not fit")
	}
	// Another master with longer gaps: every track a little long.
	if !Fits(album(48, 161, 179, 209, 20, 231, 245, 210, 204, 207, 24, 214, 9, 217, 257), creeper) {
		t.Error("every track about 2s long should fit")
	}
	if Fits(album(200, 185), want(200, 180)) {
		t.Error("a short release allows no outlier")
	}
}

func TestFitsRealMismatches(t *testing.T) {
	// MusicBrainz minus Tidal, in tenths of a second, from albums held for
	// review. isrcs says whether each MusicBrainz track
	// carries the Tidal track's ISRC.
	cases := []struct {
		name  string
		diffs []int
		isrcs bool
		fits  bool
	}{
		// Every track 1-3s long on Tidal: a master with longer gaps.
		{"Royal Bangs — Let It Beep", []int{-30, -30, -30, -20, -30, -30, -20, -30, -20, -30, -20, -10}, false, true},
		// The boundary between tracks 10 and 11 moved.
		{"Scuba — Triangulation", []int{0, 0, -10, -10, 0, -10, 0, -20, 10, 70, -80, -10}, false, true},
		{"The Burns Unit — Side Show", []int{-1, 1, 0, -4, 0, -5, -2, -5, 250, -223, 10}, false, true},
		// The same recordings, measured differently.
		{"A Winged Victory For The Sullen — Atomos", []int{0, 0, 1, 0, -4, 2, 1, 0, 1, -1, -311}, true, true},
		{"Bassekou Kouyate — I Speak Fula", []int{-4, 0, 0, 1, 3, 4, 3, 2, -4, -2, 1664}, true, true},
		// Without ISRCs, one track 31s out is a different version.
		{"Atomos without ISRCs", []int{0, 0, 1, 0, -4, 2, 1, 0, 1, -1, -311}, false, false},
	}
	for _, c := range cases {
		var r Release
		var w Want
		var tracks []Track
		for i, d := range c.diffs {
			secs := 200 + i
			isrc := fmt.Sprintf("X%02d", i)
			tr := Track{Length: secs*1000 + d*100}
			if c.isrcs {
				tr.Recording.ISRCs = []string{isrc}
			}
			tracks = append(tracks, tr)
			w.Tracks = append(w.Tracks, WantTrack{Seconds: secs, ISRC: isrc})
		}
		r.Media = []Medium{{Tracks: tracks}}
		if got := Fits(r, w); got != c.fits {
			t.Errorf("%s: Fits = %v, want %v", c.name, got, c.fits)
		}
	}

	// One disagreeing ISRC is a different recording, however close the lengths.
	r := Release{Media: []Medium{{Tracks: []Track{
		{Length: 200_000, Recording: Recording{ISRCs: []string{"A"}}},
		{Length: 200_000, Recording: Recording{ISRCs: []string{"B"}}},
		{Length: 200_000, Recording: Recording{ISRCs: []string{"Z"}}},
		{Length: 260_000, Recording: Recording{ISRCs: []string{"D"}}},
	}}}}
	w := Want{Tracks: []WantTrack{{200, "A"}, {200, "B"}, {200, "C"}, {200, "D"}}}
	if Fits(r, w) {
		t.Error("a mismatched ISRC must not excuse a 60s difference")
	}
}

func TestBestPrefersISRCsThenCountryAndFormat(t *testing.T) {
	want := Want{Tracks: []WantTrack{{ISRC: "X1"}}}
	mk := func(id, country, format, status, date, isrc string) Release {
		return Release{ID: id, Country: country, Status: status, Date: date,
			Media: []Medium{{Format: format, Tracks: []Track{{Recording: Recording{ISRCs: []string{isrc}}}}}}}
	}
	us := mk("us", "US", "CD", "Official", "2015-01-01", "X1")
	gb := mk("gb", "GB", "Digital Media", "Official", "2016-01-01", "X1")
	jp := mk("jp", "JP", "Digital Media", "Official", "2010-01-01", "X1")
	noISRC := mk("gb2", "GB", "Digital Media", "Official", "2010-01-01", "")
	if got := Best([]Release{us, jp, noISRC, gb}, want, DefaultPrefs); got.ID != "gb" {
		t.Errorf("Best = %s, want gb", got.ID)
	}
}

func TestRomanNumerals(t *testing.T) {
	for in, want := range map[string]string{
		"Angels Of Darkness, Demons Of Light 1": "Angels Of Darkness, Demons Of Light I",
		"Run The Jewels 2":                      "Run The Jewels II",
		"1999":                                  "1999",
		"Getting Killed":                        "Getting Killed",
	} {
		if got := romanNumerals(in); got != want {
			t.Errorf("romanNumerals(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoreTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Devotion - The Gold Edition":           "Devotion",
		"{Awayland} (Digital Deluxe)":           "{Awayland}",
		"Hadestown: Deluxe":                     "Hadestown",
		"Angels Of Darkness, Demons Of Light 1": "Angels Of Darkness, Demons Of Light 1",
		"Sex, Death & The Infinite Void":        "Sex, Death & The Infinite Void",
		"Hand. Cannot. Erase. - Steven Wilson":  "Hand. Cannot. Erase. - Steven Wilson",
	} {
		if got := coreTitle(in); got != want {
			t.Errorf("coreTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
