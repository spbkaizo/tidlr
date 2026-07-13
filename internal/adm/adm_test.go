package adm

import (
	"os"
	"testing"
)

func TestParseFixture(t *testing.T) {
	f, err := os.Open("testdata/justout.html")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	releases, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The captured fixture holds 17 distinct albums (publication cross-links
	// must not leak in as albums). Assert a healthy floor.
	if len(releases) < 15 {
		t.Fatalf("expected >=15 releases, got %d", len(releases))
	}

	// Every release must have the fields we depend on downstream.
	ids := make(map[int]bool)
	for _, r := range releases {
		if r.ReviewID == 0 {
			t.Errorf("release %q - %q has zero ReviewID", r.Artist, r.Album)
		}
		if r.Artist == "" || r.Album == "" {
			t.Errorf("release id %d missing artist/album: %+v", r.ReviewID, r)
		}
		if ids[r.ReviewID] {
			t.Errorf("duplicate ReviewID %d not deduped", r.ReviewID)
		}
		ids[r.ReviewID] = true
	}

	// Spot-check a known entry from the captured fixture.
	var found bool
	for _, r := range releases {
		if r.ReviewID == 14651 {
			found = true
			if r.Artist != "The Rolling Stones" {
				t.Errorf("id 14651 artist = %q, want The Rolling Stones", r.Artist)
			}
			if r.Album != "Foreign Tongues" {
				t.Errorf("id 14651 album = %q, want Foreign Tongues", r.Album)
			}
			if r.SearchQuery() != "The Rolling Stones Foreign Tongues" {
				t.Errorf("SearchQuery = %q", r.SearchQuery())
			}
		}
	}
	if !found {
		t.Error("expected known release id 14651 in fixture")
	}
}
