// Package tagger writes Picard-style tags into .m4a files: the standard iTunes
// atoms plus the "----:com.apple.iTunes:<name>" freeform atoms where Picard
// keeps MusicBrainz ids and its other extended tags, under the same names, so
// Picard (and anything else reading Picard's tags) sees the album as matched.
//
// Writing goes through AtomicParsley, since ffmpeg cannot write freeform
// atoms. AtomicParsley keeps one value per freeform name, so multi-valued tags
// (several artist ids, release types) are joined with "; ".
package tagger

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Tags is everything written to one track.
type Tags struct {
	Title, Artist, AlbumArtist, Album string
	Date                              string
	Genre, Grouping                   string
	Copyright                         string
	SortArtist, SortAlbumArtist       string
	Track, TrackTotal                 int
	Disc, DiscTotal                   int
	// Freeform are the extended tags, in write order.
	Freeform []Field
}

// Field is one freeform tag.
type Field struct{ Name, Value string }

// MultiSep joins multiple values into one freeform value.
const MultiSep = "; "

// Args returns the AtomicParsley arguments that write t to path in place.
// Empty values are skipped, so they never blank out an existing tag.
func (t Tags) Args(path string) []string {
	args := []string{path}
	add := func(flag, v string) {
		if v != "" {
			args = append(args, flag, v)
		}
	}
	add("--title", t.Title)
	add("--artist", t.Artist)
	add("--albumArtist", t.AlbumArtist)
	add("--album", t.Album)
	add("--year", t.Date)
	add("--genre", t.Genre)
	add("--grouping", t.Grouping)
	add("--copyright", t.Copyright)
	if t.Track > 0 {
		add("--tracknum", pair(t.Track, t.TrackTotal))
	}
	if t.Disc > 0 {
		add("--disk", pair(t.Disc, t.DiscTotal))
	}
	if t.SortArtist != "" {
		args = append(args, "--sortOrder", "artist", t.SortArtist)
	}
	if t.SortAlbumArtist != "" {
		args = append(args, "--sortOrder", "albumartist", t.SortAlbumArtist)
	}
	for _, f := range t.Freeform {
		if f.Value != "" {
			args = append(args, "--rDNSatom", f.Value, "name="+f.Name, "domain=com.apple.iTunes")
		}
	}
	return append(args, "--overWrite")
}

func pair(n, total int) string {
	if total > 0 {
		return strconv.Itoa(n) + "/" + strconv.Itoa(total)
	}
	return strconv.Itoa(n)
}

// Writer runs AtomicParsley.
type Writer struct {
	Bin string // default "AtomicParsley"
}

// Write tags the file at path in place.
func (w Writer) Write(ctx context.Context, path string, t Tags) error {
	bin := w.Bin
	if bin == "" {
		bin = "AtomicParsley"
	}
	cmd := exec.CommandContext(ctx, bin, t.Args(path)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("AtomicParsley %s: %w: %s", path, err, strings.TrimSpace(lastLines(out.String(), 3)))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
