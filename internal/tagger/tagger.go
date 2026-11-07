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
	"os"
	"os/exec"
	"path/filepath"
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
//
// Some files make AtomicParsley's in-place update fail ("insufficient space
// to retag the source file") although it can still write them out whole, so
// on that error the tagged file is written beside the original and renamed
// over it; the original stays untouched until the rename.
func (w Writer) Write(ctx context.Context, path string, t Tags) error {
	err := w.run(ctx, t.Args(path))
	if err == nil || !strings.Contains(err.Error(), "insufficient space to retag") {
		return wrapErr(path, err)
	}
	tmp := filepath.Join(filepath.Dir(path), ".tidlr-retag-"+filepath.Base(path))
	args := t.Args(path)
	args = append(args[:len(args)-1], "--output", tmp) // replace --overWrite
	if err := w.run(ctx, args); err != nil {
		os.Remove(tmp)
		return wrapErr(path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (w Writer) run(ctx context.Context, args []string) error {
	bin := w.Bin
	if bin == "" {
		bin = "AtomicParsley"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(lastLines(out.String(), 3)))
	}
	return nil
}

func wrapErr(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("AtomicParsley %s: %w", path, err)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
