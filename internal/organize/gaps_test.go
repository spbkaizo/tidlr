package organize

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/tidal"
)

func TestWithGaps(t *testing.T) {
	tr := func(id int64, disc, n int) tidal.Track {
		return tidal.Track{ID: id, VolumeNumber: disc, TrackNumber: n}
	}
	ids := func(ts []tidal.Track) []int64 {
		var out []int64
		for _, t := range ts {
			out = append(out, t.ID)
		}
		return out
	}
	// undun: track 11 of 14 missing.
	var undun []tidal.Track
	for n := 1; n <= 14; n++ {
		if n != 11 {
			undun = append(undun, tr(int64(n), 1, n))
		}
	}
	got := withGaps(undun, 14)
	if len(got) != 14 || got[10].ID != 0 || got[11].ID != 12 {
		t.Errorf("undun: %v", ids(got))
	}
	// The last track missing.
	if got := withGaps([]tidal.Track{tr(1, 1, 1), tr(2, 1, 2)}, 3); len(got) != 3 || got[2].ID != 0 {
		t.Errorf("missing last: %v", ids(got))
	}
	// Two discs, a gap on the first.
	got = withGaps([]tidal.Track{tr(1, 1, 1), tr(3, 1, 3), tr(4, 2, 1), tr(5, 2, 2)}, 5)
	if want := []int64{1, 0, 3, 4, 5}; len(got) != 5 || got[1].ID != 0 || got[3].ID != 4 {
		t.Errorf("two discs: %v, want %v", ids(got), want)
	}
	// Nothing missing, or numbers that cannot be placed.
	if withGaps([]tidal.Track{tr(1, 1, 1)}, 1) != nil {
		t.Error("complete album should give nil")
	}
	if withGaps([]tidal.Track{tr(1, 1, 1), tr(2, 1, 5)}, 3) != nil {
		t.Error("track 5 of 3 should give nil")
	}
	if withGaps([]tidal.Track{tr(1, 1, 1), tr(2, 1, 1)}, 3) != nil {
		t.Error("duplicate number should give nil")
	}
}

type fakeFiller struct{ have map[string]bool }

func (f fakeFiller) Fill(_ context.Context, dir string, _ tidal.Album, volume, number int, want tidal.WantTrack) (tidal.Track, error) {
	if !f.have[want.Title] {
		return tidal.Track{}, tidal.ErrNoMatch
	}
	tr := tidal.Track{ID: 99, Title: want.Title, VolumeNumber: volume, TrackNumber: number, ISRC: want.ISRCs[0]}
	return tr, os.WriteFile(filepath.Join(dir, tidal.TrackFilename(tr)+".m4a"), nil, 0o644)
}

func TestFillGaps(t *testing.T) {
	dir := t.TempDir()
	r := musicbrainz.Release{Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.Track{
		{Position: 1, Title: "Mount the Air", Recording: musicbrainz.Recording{ISRCs: []string{"I1"}}},
		{Position: 2, Title: "Flutter"},
		{Position: 3, Title: "Valentine", Length: 130_400, Recording: musicbrainz.Recording{ISRCs: []string{"I3"}}},
	}}}}
	tracks := []tidal.Track{{}, {ID: 2, Title: "Flutter", TrackNumber: 2}, {}}
	files := []string{"", "flutter.m4a", ""}
	o := &Organizer{Filler: fakeFiller{have: map[string]bool{"Valentine": true}}}
	missing := o.fillGaps(context.Background(), Album{Dir: dir}, r, tracks, files)
	if len(missing) != 1 || missing[0] != "1 Mount the Air" {
		t.Errorf("missing = %q, want [1 Mount the Air]", missing)
	}
	if tracks[2].ID != 99 || tracks[2].ISRC != "I3" || filepath.Base(files[2]) != "03 - Valentine.m4a" {
		t.Errorf("filled slot: %+v %q", tracks[2], files[2])
	}

	// Without a filler every blank is reported.
	tracks = []tidal.Track{{}, {ID: 2}, {}}
	if missing := (&Organizer{}).fillGaps(context.Background(), Album{Dir: dir}, r, tracks, make([]string, 3)); len(missing) != 2 {
		t.Errorf("no filler: missing %q", missing)
	}
}
