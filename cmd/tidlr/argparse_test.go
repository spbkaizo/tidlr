package main

import (
	"reflect"
	"testing"
)

// TestAlbumArgsFromCommandLine covers the reported bug: `-album URL URL ...`
// passed every URL after the first as a bare positional argument, which was
// silently dropped, so only the first album downloaded.
func TestAlbumArgsFromCommandLine(t *testing.T) {
	flagged := "https://tidal.com/album/560221929/u"
	rest := "https://tidal.com/album/535577394/u https://tidal.com/album/561764730/u " +
		"https://tidal.com/album/560201305/u https://tidal.com/album/561763543/u " +
		"https://tidal.com/album/560221570/u https://tidal.com/album/549111544/u " +
		"https://tidal.com/album/533982947/u"

	got := parseAlbumIDs(joinArgs(flagged, rest))
	want := []int64{560221929, 535577394, 561764730, 560201305, 561763543, 560221570, 549111544, 533982947}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %d ids %v, want %d ids %v", len(got), got, len(want), want)
	}
}

func TestJoinArgs(t *testing.T) {
	for _, tt := range []struct{ flagged, rest, want string }{
		{"a", "b c", "a b c"},
		{"a", "", "a"},
		{"", "b c", "b c"},
		{"", "", ""},
	} {
		if got := joinArgs(tt.flagged, tt.rest); got != tt.want {
			t.Errorf("joinArgs(%q,%q) = %q, want %q", tt.flagged, tt.rest, got, tt.want)
		}
	}
}

// TestDedupeIDs: the same album listed twice must be fetched once.
func TestDedupeIDs(t *testing.T) {
	got := dedupeIDs([]int64{5, 3, 5, 9, 3})
	want := []int64{5, 3, 9}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupeIDs = %v, want %v", got, want)
	}
	if got := dedupeIDs(nil); len(got) != 0 {
		t.Errorf("dedupeIDs(nil) = %v, want empty", got)
	}
}
