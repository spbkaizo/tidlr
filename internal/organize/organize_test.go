package organize

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simonb/tidlr/internal/filing"
	"github.com/simonb/tidlr/internal/genre"
	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/musicbrainz/mbtest"
	"github.com/simonb/tidlr/internal/tagger"
	"github.com/simonb/tidlr/internal/tidal"
)

var mbData = filepath.Join("..", "musicbrainz", "testdata")

// downloaded lays out a converted album as the pipeline leaves it: one short
// ALAC file per Tidal track, named tidal.TrackFilename, plus cover.jpg.
func downloaded(t *testing.T, tidalID string) Album {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "AtomicParsley"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	b, err := os.ReadFile(filepath.Join(mbData, "tidal", tidalID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Album tidal.Album      `json:"album"`
		Items tidal.AlbumItems `json:"items"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "scratch", fx.Album.ArtistName(), fx.Album.Title)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src.m4a")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:a", "alac", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	audio, _ := os.ReadFile(src)
	a := Album{Dir: dir, Tidal: fx.Album}
	for _, it := range fx.Items.Items {
		a.Tracks = append(a.Tracks, it.Item)
		if err := os.WriteFile(filepath.Join(dir, tidal.TrackFilename(it.Item)+".m4a"), audio, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	return a
}

func organizer(t *testing.T) *Organizer {
	mb := musicbrainz.New("tidlr", "test")
	mb.HTTP = &http.Client{Transport: mbtest.Replay{Dir: filepath.Join(mbData, "http"), T: t}}
	mb.Interval = 1
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "library"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Organizer{
		MB: mb, Prefs: musicbrainz.DefaultPrefs, Lists: genre.Builtin(), Tagger: tagger.Writer{},
		LibraryDir: filepath.Join(root, "library"), ReviewDir: filepath.Join(root, "review"),
	}
}

func TestOrganizeFilesTaggedAlbum(t *testing.T) {
	a := downloaded(t, "440082667") // Geese — Getting Killed
	o := organizer(t)
	dest, err := o.Organize(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	// No Last.fm here, so the grouping comes from the release group's
	// MusicBrainz genres through the ID3 grouping list.
	if want := filepath.Join(o.LibraryDir, "Archive Albums", "Psychedelic Rock", "Geese - 2025 - Getting Killed"); dest != want {
		t.Errorf("filed at %s, want %s", dest, want)
	}
	entries, _ := os.ReadDir(dest)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 12 || names[0] != "01 - Trinidad.m4a" || names[len(names)-1] != "cover.jpg" {
		t.Errorf("album folder holds %q", names)
	}
	if _, err := os.Stat(filepath.Dir(a.Dir)); !os.IsNotExist(err) {
		t.Error("scratch album or emptied artist folder left behind")
	}
	out, _ := exec.Command("AtomicParsley", filepath.Join(dest, "01 - Trinidad.m4a"), "-t").CombinedOutput()
	for _, want := range []string{
		"MusicBrainz Album Id] contains: 2b637090-036e-4f86-8183-e1be856e1421",
		`Atom "©nam" contains: Trinidad`,
		`Atom "trkn" contains: 1 of 11`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("tags missing %q:\n%s", want, out)
		}
	}

	// The same album again is not overwritten: it is held for review.
	again := downloaded(t, "440082667")
	_, err = o.Organize(context.Background(), again)
	var rev *ReviewError
	if !errors.As(err, &rev) || !strings.Contains(rev.Reason, "already in the library") {
		t.Fatalf("second copy: err = %v, want review (already in the library)", err)
	}
	if _, err := os.Stat(filepath.Join(rev.Dir, "01 - Trinidad.m4a")); err != nil {
		t.Errorf("review copy missing: %v", err)
	}
}

func TestOrganizeStopsWhenLibraryUnmounted(t *testing.T) {
	a := downloaded(t, "440082667")
	o := organizer(t)
	o.LibraryDir = filepath.Join(o.LibraryDir, "share-gone")
	_, err := o.Organize(context.Background(), a)
	if !errors.Is(err, filing.ErrNotMounted) {
		t.Fatalf("err = %v, want filing.ErrNotMounted", err)
	}
	// Nothing moved: the album is still where convert left it, and nothing
	// went to review or was created where the library should be.
	if _, err := os.Stat(filepath.Join(a.Dir, "01 - Trinidad.m4a")); err != nil {
		t.Errorf("album moved away: %v", err)
	}
	for _, d := range []string{o.LibraryDir, o.ReviewDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s was created", d)
		}
	}
}
