package main

import (
	"strings"
	"testing"
	"time"

	"github.com/simonb/tidlr/internal/queue"
)

func TestFormatReport(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	acts := []queue.Activity{
		{Artist: "Vince Staples", Album: "Summertime '06", State: queue.StateDone,
			Dir: "/lib/Archive Albums/Hip-Hop/Vince Staples - 2015 - Summertime '06"},
		{Artist: "D'Angelo", Album: "Black Messiah", State: queue.StateReview,
			Detail: "held for review (no MusicBrainz release matches) at /review/D'Angelo/Black Messiah"},
		{Artist: "Somebody", Album: "Garbled", State: queue.StateSkipped},
		{Artist: "X", Album: "Y", State: queue.StateFailed, Detail: "connection reset\nmore"},
	}
	got := formatReport(acts, "/lib", now.AddDate(0, 0, -7), now)
	for _, want := range []string{
		"tidlr: Sun 1 Nov to Sun 8 Nov 2026",
		"Filed (1)\n  Hip-Hop                Vince Staples — Summertime '06",
		"Held for review (1) — tidlr file DIR RELEASE files one\n  D'Angelo — Black Messiah: no MusicBrainz release matches\n      /review/D'Angelo/Black Messiah",
		"Not found on Tidal (1)\n  Somebody — Garbled",
		"Failed (1) — tidlr retry tries again\n  X — Y: connection reset\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := formatReport(nil, "/lib", now.AddDate(0, 0, -7), now); !strings.Contains(got, "Nothing new.") {
		t.Errorf("empty report:\n%s", got)
	}
}

func TestLibraryFolder(t *testing.T) {
	for _, c := range [][3]string{
		{"/lib", "/lib/Archive Albums/Hip-Hop/A - 2020 - B", "Hip-Hop"},
		{"/lib", "/lib/Geese/2025 - Getting Killed", "Geese"},
		{"/lib", "/elsewhere/A/B", ""},
		{"", "/lib/x/y", ""},
	} {
		if got := libraryFolder(c[0], c[1]); got != c[2] {
			t.Errorf("libraryFolder(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
