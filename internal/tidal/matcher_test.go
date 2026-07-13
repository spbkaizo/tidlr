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
