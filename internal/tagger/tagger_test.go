package tagger

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/simonb/tidlr/internal/genre"
	"github.com/simonb/tidlr/internal/musicbrainz"
)

func credit(name, sort, id, join string) musicbrainz.Credit {
	c := musicbrainz.Credit{Name: name, JoinPhrase: join}
	c.Artist.ID, c.Artist.Name, c.Artist.SortName = id, name, sort
	return c
}

// release is a two-disc release with a featured artist on disc 2.
func release() musicbrainz.Release {
	r := musicbrainz.Release{
		ID: "rel-1", Title: "Have One on Me", Status: "Official", Country: "XW", Date: "2010-02-23", Barcode: "781484039020",
		ArtistCredit:       []musicbrainz.Credit{credit("Joanna Newsom", "Newsom, Joanna", "art-1", "")},
		LabelInfo:          []musicbrainz.LabelInfo{{CatalogNumber: "DC390"}},
		ReleaseGroup:       musicbrainz.ReleaseGroup{ID: "rg-1", PrimaryType: "Album", FirstReleaseDate: "2010-02-23"},
		TextRepresentation: musicbrainz.TextRepr{Script: "Latn"},
	}
	r.LabelInfo[0].Label.Name = "Drag City"
	r.Media = []musicbrainz.Medium{
		{Position: 1, Format: "Digital Media", Tracks: []musicbrainz.Track{
			{ID: "t-1", Position: 1, Title: "Easy", Recording: musicbrainz.Recording{ID: "rec-1"}},
			{ID: "t-2", Position: 2, Title: "Have One on Me", Recording: musicbrainz.Recording{ID: "rec-2"}},
		}},
		{Position: 2, Format: "Digital Media", Tracks: []musicbrainz.Track{
			{ID: "t-3", Position: 1, Title: "Duet", Recording: musicbrainz.Recording{ID: "rec-3", ISRCs: []string{"US58L1039003"}},
				ArtistCredit: []musicbrainz.Credit{credit("Joanna Newsom", "Newsom, Joanna", "art-1", " feat. "), credit("Bill Callahan", "Callahan, Bill", "art-2", "")}},
		}},
	}
	return r
}

func field(t Tags, name string) string {
	for _, f := range t.Freeform {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestFromRelease(t *testing.T) {
	g := genre.Result{Grouping: "Folk", Genres: []string{"Folk", "Singer-Songwriter"}, Moods: []string{"Melancholy"}}
	tags, err := FromRelease(release(), 2, g, Source{Copyright: "℗ 2010 Drag City", ReplayGain: -7.5, Peak: 0.98})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][2]string{
		"title":            {tags.Title, "Duet"},
		"artist":           {tags.Artist, "Joanna Newsom feat. Bill Callahan"},
		"albumartist":      {tags.AlbumArtist, "Joanna Newsom"},
		"sortartist":       {tags.SortArtist, "Newsom, Joanna feat. Callahan, Bill"},
		"genre":            {tags.Genre, "Folk; Singer-Songwriter"},
		"artist ids":       {field(tags, "MusicBrainz Artist Id"), "art-1; art-2"},
		"ARTISTS":          {field(tags, "ARTISTS"), "Joanna Newsom; Bill Callahan"},
		"recording id":     {field(tags, "MusicBrainz Track Id"), "rec-3"},
		"release track id": {field(tags, "MusicBrainz Release Track Id"), "t-3"},
		"ISRC from MB":     {field(tags, "ISRC"), "US58L1039003"},
		"type":             {field(tags, "MusicBrainz Album Type"), "album"},
		"status":           {field(tags, "MusicBrainz Album Status"), "official"},
		"label":            {field(tags, "LABEL"), "Drag City"},
		"originalyear":     {field(tags, "originalyear"), "2010"},
		"albumgrouping":    {field(tags, "albumgrouping"), "Folk"},
		"replaygain":       {field(tags, "replaygain_track_gain"), "-7.50 dB"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if tags.Track != 1 || tags.TrackTotal != 1 || tags.Disc != 2 || tags.DiscTotal != 2 {
		t.Errorf("track %d/%d disc %d/%d, want 1/1 disc 2/2", tags.Track, tags.TrackTotal, tags.Disc, tags.DiscTotal)
	}
	r := release()
	r.Media[0].Tracks[0].Title = "I’m Scum – Live…"
	if tags, _ := FromRelease(r, 0, g, Source{}); tags.Title != "I'm Scum - Live..." {
		t.Errorf("punctuation not converted: %q", tags.Title)
	}
	if _, err := FromRelease(release(), 3, g, Source{}); err == nil {
		t.Error("track past the end should fail")
	}
}

// TestWriteRoundTrip tags a real .m4a with AtomicParsley and reads it back.
func TestWriteRoundTrip(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "AtomicParsley"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	path := filepath.Join(t.TempDir(), "t.m4a")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "alac", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	tags, err := FromRelease(release(), 2, genre.Result{Grouping: "Folk", Genres: []string{"Folk"}}, Source{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (Writer{}).Write(context.Background(), path, tags); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("AtomicParsley", path, "-t").CombinedOutput()
	if err != nil {
		t.Fatalf("read back: %v: %s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		`Atom "©nam" contains: Duet`,
		`Atom "©ART" contains: Joanna Newsom feat. Bill Callahan`,
		`Atom "trkn" contains: 1 of 1`,
		`Atom "disk" contains: 2 of 2`,
		`[com.apple.iTunes;MusicBrainz Album Id] contains: rel-1`,
		`[com.apple.iTunes;MusicBrainz Track Id] contains: rec-3`,
		`[com.apple.iTunes;albumgrouping] contains: Folk`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

// TestWriteFallback: when the in-place update fails with "insufficient space
// to retag", the file is written out whole and renamed over the original.
func TestWriteFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as AtomicParsley")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeap")
	script := `#!/bin/sh
src=$1; out=
while [ $# -gt 0 ]; do
	case $1 in
	--overWrite) echo "AtomicParsley error: the insufficient space to retag the source file (2!=1)."; exit 1 ;;
	--output) out=$2; shift ;;
	esac
	shift
done
printf 'tagged' > "$out"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "01 - a.m4a")
	if err := os.WriteFile(path, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Writer{Bin: bin}).Write(context.Background(), path, Tags{Grouping: "Folk"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "tagged" {
		t.Errorf("file = %q, want the rewritten copy", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".tidlr-retag-*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}
