package main

import (
	"reflect"
	"testing"
)

func TestParseTrackIDs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []int64
	}{
		{"bare id", "113302335", []int64{113302335}},
		{"plain url", "https://tidal.com/track/113302335", []int64{113302335}},
		// Tidal's share links append /u; the id must win over the suffix.
		{"share url with /u", "https://tidal.com/track/113302335/u", []int64{113302335}},
		{"browse url", "https://tidal.com/browse/track/545220119", []int64{545220119}},
		{"space separated", "113302335 545220119", []int64{113302335, 545220119}},
		{"comma separated", "113302335,545220119", []int64{113302335, 545220119}},
		{"mixed urls and ids", "https://tidal.com/track/113302335/u, 545220119", []int64{113302335, 545220119}},
		{"newline separated", "113302335\n545220119", []int64{113302335, 545220119}},
		{"empty", "", nil},
		{"no digits", "not-a-track", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTrackIDs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseTrackIDs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseAlbumIDs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []int64
	}{
		{"bare id", "540168117", []int64{540168117}},
		{"plain url", "https://tidal.com/album/540168117", []int64{540168117}},
		{"share url with /u", "https://tidal.com/album/540168117/u", []int64{540168117}},
		{"multiple", "540168117 545220108", []int64{540168117, 545220108}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAlbumIDs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseAlbumIDs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
