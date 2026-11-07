package organize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/tagger"
)

// FileAs files the album in dir as MusicBrainz release mbid, for an album
// held for review that the user has identified by hand. Its tracks are put in
// order by their disc and track tags (file names if untagged) and must number
// as many as the release's. On any error the album stays in dir.
func (o *Organizer) FileAs(ctx context.Context, dir, mbid string) (string, error) {
	if err := o.checkMounted(); err != nil {
		return "", err
	}
	files, err := orderedTracks(ctx, o.Tagger, dir)
	if err != nil {
		return "", err
	}
	r, err := o.MB.LookupRelease(ctx, mbid)
	if err != nil {
		return "", fmt.Errorf("MusicBrainz release %s: %w", mbid, err)
	}
	if n := len(r.Tracks()); n != len(files) {
		return "", fmt.Errorf("release %s has %d tracks, %s has %d", mbid, n, dir, len(files))
	}
	o.logf("filing %s as MusicBrainz release %s (%s — %s)", dir, r.ID, musicbrainz.CreditName(r.ArtistCredit), r.Title)

	// Keep what the files already carry that MusicBrainz doesn't have.
	sources := make([]tagger.Source, len(files))
	for i, f := range files {
		if tags, err := o.Tagger.Read(ctx, f); err == nil {
			sources[i] = tagger.Source{ISRC: tags["ISRC"], Copyright: tags["cprt"]}
		}
	}
	return o.file(ctx, r, dir, files, sources)
}

// orderedTracks lists the audio files in dir in disc, then track order, from
// their tags, falling back to file name order.
func orderedTracks(ctx context.Context, w tagger.Writer, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type track struct {
		path        string
		disc, track int
	}
	var tracks []track
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".m4a" && ext != ".mp4") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		t := track{path: filepath.Join(dir, e.Name())}
		if tags, err := w.Read(ctx, t.path); err == nil {
			t.disc, t.track = leadingInt(tags["disk"]), leadingInt(tags["trkn"])
		}
		tracks = append(tracks, t)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("no .m4a or .mp4 files in %s", dir)
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		a, b := tracks[i], tracks[j]
		if a.track == 0 || b.track == 0 {
			return a.path < b.path
		}
		if max(a.disc, 1) != max(b.disc, 1) {
			return max(a.disc, 1) < max(b.disc, 1)
		}
		return a.track < b.track
	})
	files := make([]string, len(tracks))
	for i, t := range tracks {
		files[i] = t.path
	}
	return files, nil
}

// leadingInt reads the number at the start of "3 of 12" (0 if none).
func leadingInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), " ", 2)[0]))
	return n
}
