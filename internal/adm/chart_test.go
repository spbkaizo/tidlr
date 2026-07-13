package adm

import (
	"os"
	"testing"
	"time"
)

func TestParseChartFixture(t *testing.T) {
	f, err := os.Open("testdata/chart_p1.html")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	releases, err := ParseChart(f)
	if err != nil {
		t.Fatalf("ParseChart: %v", err)
	}
	if len(releases) < 25 {
		t.Fatalf("expected many chart releases, got %d", len(releases))
	}

	ids := map[int]bool{}
	dated := 0
	for _, r := range releases {
		if r.ReviewID == 0 || r.Artist == "" || r.Album == "" {
			t.Errorf("incomplete release: %+v", r)
		}
		if ids[r.ReviewID] {
			t.Errorf("duplicate ReviewID %d", r.ReviewID)
		}
		ids[r.ReviewID] = true
		if !r.Added.IsZero() {
			dated++
		}
	}
	if dated < 25 {
		t.Errorf("expected most releases to carry an Added date, got %d", dated)
	}

	// Spot-check a known entry and its parsed date.
	for _, r := range releases {
		if r.ReviewID == 14644 {
			if r.Artist != "Olivia Rodrigo" {
				t.Errorf("id 14644 artist = %q", r.Artist)
			}
			want := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
			if !r.Added.Equal(want) {
				t.Errorf("id 14644 Added = %v, want %v", r.Added, want)
			}
		}
	}
}

func TestParseAdded(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"Added: 12/06/2026", time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)},
		{"  Added: 01/02/2026 ", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"no date here", time.Time{}},
		{"Added: garbage", time.Time{}},
	}
	for _, c := range cases {
		got := parseAdded(c.in)
		if !got.Equal(c.want) {
			t.Errorf("parseAdded(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
