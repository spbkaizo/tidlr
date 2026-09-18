package main

import (
	"reflect"
	"strings"
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

// TestVersionString covers the two banner shapes: a stamped release tag, and an
// unstamped dev build reporting the release it is based on. The commit/go lines
// come from the toolchain's build stamp and are not asserted here, since they
// vary with how the test binary itself was built.
func TestVersionString(t *testing.T) {
	origVersion, origBase := version, baseVersion
	t.Cleanup(func() { version, baseVersion = origVersion, origBase })

	tests := []struct {
		name        string
		version     string
		baseVersion string
		wantFirst   string
	}{
		{"tagged release", "v1.8.0", "", "tidlr v1.8.0"},
		// A release build ignores baseVersion: the tag is the version.
		{"tagged ignores base", "v1.8.0", "v1.7.0", "tidlr v1.8.0"},
		{"dev with base", "", "v1.8.0", "tidlr dev (based on v1.8.0)"},
		{"dev without base", "", "", "tidlr dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, baseVersion = tt.version, tt.baseVersion
			got := versionString()
			first, _, _ := strings.Cut(got, "\n")
			// "dev without base" may still pick up a module version from the
			// test binary's own build info; accept the prefix in that case.
			if tt.name == "dev without base" {
				if !strings.HasPrefix(first, tt.wantFirst) {
					t.Errorf("versionString() first line = %q, want prefix %q", first, tt.wantFirst)
				}
				return
			}
			if first != tt.wantFirst {
				t.Errorf("versionString() first line = %q, want %q", first, tt.wantFirst)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("versionString() = %q, want trailing newline", got)
			}
		})
	}
}

// TestVCSInfoNilBuildInfo guards the path taken when a binary carries no build
// info at all: the caller must get empty values rather than a panic.
func TestVCSInfoNilBuildInfo(t *testing.T) {
	rev, date, dirty := vcsInfo(nil)
	if rev != "" || date != "" || dirty {
		t.Errorf("vcsInfo(nil) = (%q, %q, %v), want empty", rev, date, dirty)
	}
}
