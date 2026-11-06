// Package organize finishes a downloaded album the way the user's Picard
// setup did: identify it on MusicBrainz, pick its genre from Last.fm (falling
// back to MusicBrainz genres), write Picard's tags, and file it in the library
// under Picard's naming scheme. An album that can't be identified or filed is
// held in a review folder instead, with the reason.
package organize

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/simonb/tidlr/internal/filing"
	"github.com/simonb/tidlr/internal/genre"
	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/tagger"
	"github.com/simonb/tidlr/internal/tidal"
)

// Organizer tags and files albums.
type Organizer struct {
	MB     *musicbrainz.Client
	Prefs  musicbrainz.Prefs
	Lists  genre.Lists
	LastFM *genre.LastFM // nil: genres from MusicBrainz only
	Tagger tagger.Writer

	LibraryDir string // e.g. /Volumes/music/FLAC
	ReviewDir  string // albums held for review: <ReviewDir>/<Artist>/<Album>
	// LocalDir is a folder on the local disk (the home folder); a library
	// under /Volumes on the same disk means its share is not mounted.
	LocalDir string

	// Merge maps genre folders onto others ("Punk Rock" -> "Punk"), so near
	// duplicates collapse into one folder.
	Merge map[string]string
	// KeepArtistTogether files an album with the artist's other albums: the
	// genre folder holding most of them wins over the computed genre.
	KeepArtistTogether bool

	artists     *artistIndex
	artistsOnce sync.Once

	Log *log.Logger
}

// ReviewError means the album was moved to the review folder rather than
// filed; Reason says why and Dir where it now is.
type ReviewError struct {
	Reason string
	Dir    string
}

func (e *ReviewError) Error() string { return "held for review (" + e.Reason + ") at " + e.Dir }

// Album is a converted album to organize: its folder (tracks named
// tidal.TrackFilename plus extension, and cover.jpg) and Tidal's metadata.
type Album struct {
	Dir    string
	Tidal  tidal.Album
	Tracks []tidal.Track
}

// Organize identifies, tags and files a. It returns the album's library
// folder, or a *ReviewError once a has been moved to the review folder. Any
// other error (including filing.ErrNotMounted) leaves a where it was.
func (o *Organizer) Organize(ctx context.Context, a Album) (string, error) {
	if err := o.checkMounted(); err != nil {
		return "", err
	}
	tracks := append([]tidal.Track(nil), a.Tracks...)
	sort.SliceStable(tracks, func(i, j int) bool {
		if tracks[i].VolumeNumber != tracks[j].VolumeNumber {
			return tracks[i].VolumeNumber < tracks[j].VolumeNumber
		}
		return tracks[i].TrackNumber < tracks[j].TrackNumber
	})

	files := make([]string, len(tracks))
	for i, tr := range tracks {
		f, err := trackFile(a.Dir, tidal.TrackFilename(tr))
		if err != nil {
			return o.review(a, err.Error())
		}
		files[i] = f
	}

	want := musicbrainz.Want{Artist: a.Tidal.ArtistName(), Title: a.Tidal.Title, Barcode: a.Tidal.UPC}
	for _, tr := range tracks {
		want.Tracks = append(want.Tracks, musicbrainz.WantTrack{Seconds: tr.Duration, ISRC: tr.ISRC})
	}
	match, err := o.MB.Identify(ctx, want, o.Prefs)
	if errors.Is(err, musicbrainz.ErrNoMatch) {
		return o.review(a, "no MusicBrainz release matches")
	}
	if err != nil {
		return o.review(a, "MusicBrainz lookup failed: "+err.Error())
	}
	r := match.Release
	o.logf("identified %s — %s as MusicBrainz release %s (%s, by %s)",
		want.Artist, want.Title, r.ID, strings.TrimSpace(r.Country+" "+r.Date), match.Method)

	g := o.genre(ctx, r)
	o.logf("genre: %s (%s)", orUnknown(g.Grouping), strings.Join(g.Genres, ", "))

	// Folder names use the tag values as written (punctuation converted).
	o.loadArtists()
	fa := filing.Album{
		AlbumArtist:  tagger.ASCIIPunct(musicbrainz.CreditName(r.ArtistCredit)),
		Title:        tagger.ASCIIPunct(r.Title),
		Grouping:     g.Grouping,
		ReleaseTypes: r.ReleaseTypes(),
		OriginalDate: r.ReleaseGroup.FirstReleaseDate,
		Date:         r.Date,
		TotalDiscs:   len(r.Media),
	}
	// Merged genres and the artist's other albums can override the genre.
	name := filepath.Base(filing.AlbumDir(fa))
	if grouping, reason := o.decideGrouping(g, filing.Subdir(fa), name, "", ""); grouping != g.Grouping {
		if reason == "artist" {
			o.logf("filing with the artist's other albums in %s (genre says %s)", grouping, orUnknown(g.Grouping))
		}
		g.Grouping, fa.Grouping = grouping, grouping
	}

	var moves []filing.Move
	for i, tr := range tracks {
		tags, err := tagger.FromRelease(r, i, g, tagger.Source{
			ISRC: tr.ISRC, Copyright: firstNonEmpty(tr.Copyright, a.Tidal.Copyright),
			ReplayGain: tr.ReplayGain, Peak: tr.Peak,
		})
		if err != nil {
			return o.review(a, err.Error())
		}
		if err := o.Tagger.Write(ctx, files[i], tags); err != nil {
			return o.review(a, "tagging failed: "+err.Error())
		}
		name := filing.TrackFile(fa, filing.Track{Disc: tags.Disc, Number: tags.Track, Title: tags.Title})
		moves = append(moves, filing.Move{From: files[i], Name: name + filepath.Ext(files[i])})
	}
	moves = append(moves, extras(a.Dir)...)

	// The share can drop mid-batch; check again right before writing to it.
	// An unmounted library is not the album's fault: leave it where it is.
	if err := o.checkMounted(); err != nil {
		return "", err
	}
	dest := filepath.Join(o.LibraryDir, filing.AlbumDir(fa))
	if err := filing.Place(dest, moves); err != nil {
		if errors.Is(err, filing.ErrExists) {
			return o.review(a, "already in the library at "+dest)
		}
		return "", err
	}
	if o.artists != nil {
		o.artists.add(dest)
	}
	removeDir(a.Dir)
	return dest, nil
}

// genre picks the album's genres: Last.fm first, as lastfmng did, then the
// release group's MusicBrainz genres when Last.fm yields no grouping.
func (o *Organizer) genre(ctx context.Context, r musicbrainz.Release) genre.Result {
	ga := genre.Album{AlbumArtist: musicbrainz.CreditName(r.ArtistCredit), Title: r.Title}
	for _, t := range r.Tracks() {
		credit := t.ArtistCredit
		if len(credit) == 0 {
			credit = r.ArtistCredit
		}
		ga.Tracks = append(ga.Tracks, genre.Track{Artist: musicbrainz.CreditName(credit), Title: t.Title})
	}
	mbGenres := func() []musicbrainz.Genre { return append(r.ReleaseGroup.Genres, r.Genres...) }
	g, _, err := o.genreFor(ctx, ga, mbGenres, true)
	if err != nil {
		// A new album must be filed somewhere: go on with MusicBrainz genres.
		o.logf("warning: Last.fm genres: %v; using MusicBrainz genres", err)
		g, _, _ = o.genreFor(ctx, ga, mbGenres, false)
	}
	return g
}

// genreFor runs Last.fm for ga, then falls back to the MusicBrainz genres
// mbGenres returns (called only if needed). source says which decided the
// grouping: "last.fm", "musicbrainz" or "" when neither had one.
//
// A Last.fm failure (as opposed to Last.fm having no tags) is returned as an
// error rather than quietly falling back: a network blip must not change an
// album's genre. Callers that must decide anyway retry with useLastFM false.
func (o *Organizer) genreFor(ctx context.Context, ga genre.Album, mbGenres func() []musicbrainz.Genre, useLastFM bool) (g genre.Result, source string, err error) {
	if o.LastFM != nil && useLastFM {
		if g, err = o.Lists.ForAlbum(ctx, o.LastFM, ga); err != nil {
			return genre.Result{}, "", err
		}
		if g.Grouping != "" {
			return g, "last.fm", nil
		}
	}
	var tags []genre.Tag
	for _, mg := range mbGenres() {
		tags = append(tags, genre.Tag{Name: mg.Name, Score: float64(max(mg.Count, 1))})
	}
	mb := o.Lists.FromTags(tags)
	g.Grouping = mb.Grouping
	if len(g.Genres) == 0 {
		g.Genres = mb.Genres
	}
	if g.Grouping != "" {
		source = "musicbrainz"
	}
	return g, source, nil
}

// review moves the album folder to the review area and reports why.
func (o *Organizer) review(a Album, reason string) (string, error) {
	artist, album := filepath.Base(filepath.Dir(a.Dir)), filepath.Base(a.Dir)
	dest := filepath.Join(o.ReviewDir, artist, album)
	var moves []filing.Move
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			moves = append(moves, filing.Move{From: filepath.Join(a.Dir, e.Name()), Name: e.Name()})
		}
	}
	if err := filing.Place(dest, moves); err != nil {
		return "", fmt.Errorf("holding for review (%s): %w", reason, err)
	}
	removeDir(a.Dir)
	o.logf("held for review: %s — %s: %s", artist, album, reason)
	return "", &ReviewError{Reason: reason, Dir: dest}
}

// trackFile finds a track's file by base name, whatever the extension the
// converter gave it (.m4a for ALAC or AAC, .mp4 for Dolby Atmos).
func trackFile(dir, base string) (string, error) {
	for _, ext := range []string{".m4a", ".mp4"} {
		p := filepath.Join(dir, base+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("track file %q not found", base)
}

// extras are the non-audio files Picard moves with an album
// (move_additional_files): cover art and lyrics.
func extras(dir string) []filing.Move {
	var out []filing.Move
	for _, pattern := range []string{"*.jpg", "*.png", "*.lrc", "*.txt"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, m := range matches {
			out = append(out, filing.Move{From: m, Name: filepath.Base(m)})
		}
	}
	return out
}

// checkMounted verifies the library share is mounted and writable.
func (o *Organizer) checkMounted() error { return filing.CheckMounted(o.LibraryDir, o.LocalDir) }

// removeDir deletes an album's working folder, then its artist folder if that
// is now empty (Picard's delete_empty_dirs).
func removeDir(dir string) {
	os.RemoveAll(dir)
	os.Remove(filepath.Dir(dir)) // fails, harmlessly, unless empty
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}

func (o *Organizer) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log.Printf(format, args...)
	}
}
