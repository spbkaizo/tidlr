package main

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/simonb/tidlr/internal/queue"
)

// mustReport prints what happened to albums in the last days days: filed,
// held for review, not found on Tidal, failed.
func mustReport(q *queue.Queue, libraryDir string, days int) {
	now := time.Now()
	since := now.AddDate(0, 0, -days)
	acts, err := q.ActivitySince(since)
	if err != nil {
		log.Fatalf("report: %v", err)
	}
	fmt.Print(formatReport(acts, libraryDir, since, now))
}

// reviewRe splits a review reason: "held for review (<reason>) at <dir>".
var reviewRe = regexp.MustCompile(`^held for review \((.*)\) at (.+)$`)

// formatReport renders activity as plain text for a terminal or an email.
func formatReport(acts []queue.Activity, libraryDir string, since, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tidlr: %s to %s\n", since.Format("Mon 2 Jan"), now.Format("Mon 2 Jan 2006"))

	group := map[queue.State][]queue.Activity{}
	for _, a := range acts {
		group[a.State] = append(group[a.State], a)
	}
	if len(acts) == 0 {
		b.WriteString("\nNothing new.\n")
		return b.String()
	}

	section := func(title, hint string, list []queue.Activity, line func(queue.Activity) string) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s (%d)", title, len(list))
		if hint != "" {
			b.WriteString(" — " + hint)
		}
		b.WriteString("\n")
		for _, a := range list {
			b.WriteString("  " + line(a) + "\n")
		}
	}
	name := func(a queue.Activity) string { return a.Artist + " — " + a.Album }

	section("Filed", "", group[queue.StateDone], func(a queue.Activity) string {
		if folder := libraryFolder(libraryDir, a.Dir); folder != "" {
			return fmt.Sprintf("%-22s %s", folder, name(a))
		}
		return name(a)
	})
	section("Held for review", "tidlr file DIR RELEASE files one", group[queue.StateReview], func(a queue.Activity) string {
		if m := reviewRe.FindStringSubmatch(a.Detail); m != nil {
			return fmt.Sprintf("%s: %s\n      %s", name(a), m[1], m[2])
		}
		return fmt.Sprintf("%s: %s", name(a), a.Detail)
	})
	section("Not found on Tidal", "", group[queue.StateSkipped], name)
	section("Failed", "tidlr retry tries again", group[queue.StateFailed], func(a queue.Activity) string {
		return fmt.Sprintf("%s: %s", name(a), firstLine(a.Detail))
	})
	return b.String()
}

// libraryFolder is where in the library an album was filed, without the album
// folder itself: its genre folder in the genre layout ("Hip-Hop"), its artist
// folder in the artist layout. "" if dir is not under libraryDir.
func libraryFolder(libraryDir, dir string) string {
	if libraryDir == "" || dir == "" {
		return ""
	}
	rel, err := filepath.Rel(libraryDir, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parent := filepath.Dir(rel)
	if parent == "." {
		return ""
	}
	return filepath.Base(parent)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
