package tidal

import (
	"testing"
	"time"
)

func album(id int64, title, artist string, modes []string, quality string, explicit bool, release string) Album {
	return Album{
		ID: id, Title: title,
		Artist:       Artist{ID: 1, Name: artist},
		AudioModes:   modes,
		AudioQuality: quality,
		Explicit:     explicit,
		ReleaseDate:  release,
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Roses (Deluxe) [STEREO]": "roses",
		"GUTS: spilled!":          "guts spilled",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLossless(t *testing.T) {
	if !isLossless(album(1, "x", "a", []string{"STEREO"}, "LOSSLESS", false, "")) {
		t.Error("STEREO LOSSLESS should be lossless")
	}
	if isLossless(album(2, "x", "a", []string{"DOLBY_ATMOS"}, "LOSSLESS", false, "")) {
		t.Error("Atmos-only should NOT be lossless")
	}
	if !isLossless(album(3, "x", "a", []string{"STEREO", "DOLBY_ATMOS"}, "HI_RES_LOSSLESS", false, "")) {
		t.Error("STEREO+Atmos HiRes should be lossless")
	}
}

func TestEditionRankPreference(t *testing.T) {
	atmos := album(1, "t", "a", []string{"DOLBY_ATMOS"}, "LOSSLESS", true, "2026-01-01")
	stereoClean := album(2, "t", "a", []string{"STEREO"}, "LOSSLESS", false, "2026-01-01")
	stereoExplicit := album(3, "t", "a", []string{"STEREO"}, "LOSSLESS", true, "2026-01-01")
	stereoExplicitNewer := album(4, "t", "a", []string{"STEREO"}, "LOSSLESS", true, "2026-06-01")

	if !editionRank(stereoClean).better(editionRank(atmos)) {
		t.Error("lossless should beat atmos")
	}
	if !editionRank(stereoExplicit).better(editionRank(stereoClean)) {
		t.Error("explicit should beat clean")
	}
	if !editionRank(stereoExplicitNewer).better(editionRank(stereoExplicit)) {
		t.Error("newer should beat older")
	}
}

func TestNameScore(t *testing.T) {
	al := album(1, "you seem pretty sad for a girl so in love", "Olivia Rodrigo", []string{"STEREO"}, "LOSSLESS", true, "")
	good := nameScore(al, "Olivia Rodrigo", "You Seem Pretty Sad for a Girl So in Love")
	if good < 0.9 {
		t.Errorf("exact-ish match score = %.3f, want > 0.9", good)
	}
	bad := nameScore(al, "Metallica", "Master of Puppets")
	if bad > 0.4 {
		t.Errorf("wrong-album score = %.3f, want < 0.4", bad)
	}
}

func TestSameArtist(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		// Real wrong pick: an exact title by someone else.
		{"Pale Honey", "Jessie Ware", false},
		{"Jessie Ware", "Jessie Ware", true},
		{"Rhiannon Giddens with Francesco Turrisi", "Rhiannon Giddens", true},
		{"ANOHNI and the Johnsons", "Anohni", true},
		{"Nick Cave", "Nick Cave & Warren Ellis", true},
		{"Run the Jewels", "Run The Jewels", true},
		{"Earth", "Earth, Wind & Fire", true},
		{"Metallica", "Olivia Rodrigo", false},
	}
	for _, c := range cases {
		a := album(1, "x", c.got, []string{"STEREO"}, "LOSSLESS", false, "")
		if got := sameArtist(a, c.want); got != c.ok {
			t.Errorf("sameArtist(%q, %q) = %v, want %v", c.got, c.want, got, c.ok)
		}
	}
	// Any credited artist counts, not just the first.
	a := album(1, "x", "Warren Ellis", nil, "", false, "")
	a.Artists = append(a.Artists, a.Artist)
	a.Artists[0].Name = "Nick Cave"
	if !sameArtist(a, "Nick Cave") {
		t.Error("second credited artist not considered")
	}
}

func TestReleaseTime(t *testing.T) {
	got := album(1, "x", "a", nil, "", false, "2026-06-12").ReleaseTime()
	want := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ReleaseTime = %v, want %v", got, want)
	}
	if !album(1, "x", "a", nil, "", false, "").ReleaseTime().IsZero() {
		t.Error("empty releaseDate should give zero time")
	}
}

func TestSearchQueries(t *testing.T) {
	got := searchQueries("Fiona Apple", "The Idler Wheel Is Wiser Than The Driver Of The Screw")
	want := []string{
		"Fiona Apple The Idler Wheel Is Wiser Than The Driver Of The Screw",
		"Fiona Apple The Idler Wheel Is",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("long title: got %q, want %q", got, want)
	}
	// A short title needs no fallback.
	if got := searchQueries("Geese", "Getting Killed"); len(got) != 1 || got[0] != "Geese Getting Killed" {
		t.Errorf("short title: got %q", got)
	}
}

func TestAcceptable(t *testing.T) {
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		title, release, want string
		ok                   bool
	}{
		// Real wrong picks.
		{"Devotion (WiMP Exclusive Interview)", "2012-08-28", "Devotion", false},
		{"blómi (Edit)", "2023-10-11", "blómi", false},
		{"Run The Jewels 3 (Instrumentals)", "2016-12-25", "Run The Jewels 2", false},
		{"Run the Jewels 3", "2017-01-20", "Run The Jewels", false},
		{"Atrocity Exhibition (10 Year Anniversary Edition)", "2026-11-13", "Atrocity Exhibition", false},
		// What they should have been.
		{"Devotion", "2012-08-20", "Devotion", true},
		{"blómi", "2023-11-03", "blómi", true},
		{"Run the Jewels 2", "2014-10-24", "Run The Jewels 2", true},
		{"Atrocity Exhibition", "2016-09-27", "Atrocity Exhibition", true},
		// Editions and asked-for versions still pass.
		{"Visitor (Deluxe Edition)", "2025-01-01", "Visitor", true},
		{"Roses (Remastered)", "2025-01-01", "Roses", true},
		{"Live at Leeds (Live)", "1970-05-16", "Live at Leeds", true},
		{"Sequel (Instrumentals)", "2020-01-01", "Sequel Instrumentals", true},
		{"Tomorrows III", "2023-01-01", "Tomorrows II", false},
		{"m b v", "2013-02-02", "mbv", true},
	}
	for _, c := range cases {
		a := album(1, c.title, "a", []string{"STEREO"}, "LOSSLESS", false, c.release)
		if got := acceptable(a, c.want, now); got != c.ok {
			t.Errorf("acceptable(%q for %q) = %v, want %v", c.title, c.want, got, c.ok)
		}
	}
}
