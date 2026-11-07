package tagger

import (
	"fmt"
	"strings"

	"github.com/simonb/tidlr/internal/genre"
	"github.com/simonb/tidlr/internal/musicbrainz"
)

// Source is a track's own data from the download (Tidal), which Picard would
// have preserved from the file rather than taken from MusicBrainz.
type Source struct {
	ISRC       string
	Copyright  string
	ReplayGain float64 // track gain, dB; 0 if unknown
	Peak       float64 // track peak, linear; 0 if unknown
}

// FromRelease builds the tags Picard writes for track index i (0-based, in
// disc then track order) of release r, with the album's genre result.
func FromRelease(r musicbrainz.Release, i int, g genre.Result, src Source) (Tags, error) {
	disc, medium, track, ok := locate(r, i)
	if !ok {
		return Tags{}, fmt.Errorf("release %s has no track %d", r.ID, i+1)
	}
	original := r.ReleaseGroup.FirstReleaseDate
	if original == "" {
		original = r.Date
	}
	isrc := src.ISRC
	if isrc == "" && len(track.Recording.ISRCs) > 0 {
		isrc = track.Recording.ISRCs[0]
	}
	var labels, catnos []string
	for _, li := range r.LabelInfo {
		if li.Label.Name != "" {
			labels = append(labels, li.Label.Name)
		}
		if li.CatalogNumber != "" {
			catnos = append(catnos, li.CatalogNumber)
		}
	}
	credit := track.ArtistCredit
	if len(credit) == 0 {
		credit = r.ArtistCredit
	}
	t := Tags{
		Title:           track.Title,
		Artist:          musicbrainz.CreditName(credit),
		AlbumArtist:     musicbrainz.CreditName(r.ArtistCredit),
		Album:           r.Title,
		Date:            r.Date,
		Grouping:        g.Grouping,
		Genre:           strings.Join(g.Genres, MultiSep),
		Copyright:       src.Copyright,
		SortArtist:      musicbrainz.CreditSortName(credit),
		SortAlbumArtist: musicbrainz.CreditSortName(r.ArtistCredit),
		Track:           track.Position,
		TrackTotal:      len(medium.Tracks),
		Disc:            disc,
		DiscTotal:       len(r.Media),
		Freeform: []Field{
			{"MusicBrainz Album Id", r.ID},
			{"MusicBrainz Album Artist Id", strings.Join(musicbrainz.CreditIDs(r.ArtistCredit), MultiSep)},
			{"MusicBrainz Artist Id", strings.Join(musicbrainz.CreditIDs(credit), MultiSep)},
			{"MusicBrainz Track Id", track.Recording.ID},
			{"MusicBrainz Release Track Id", track.ID},
			{"MusicBrainz Release Group Id", r.ReleaseGroup.ID},
			{"MusicBrainz Album Type", strings.Join(r.ReleaseTypes(), MultiSep)},
			{"MusicBrainz Album Status", strings.ToLower(r.Status)},
			{"MusicBrainz Album Release Country", r.Country},
			{"ARTISTS", strings.Join(musicbrainz.CreditArtists(credit), MultiSep)},
			{"BARCODE", r.Barcode},
			{"LABEL", strings.Join(labels, MultiSep)},
			{"CATALOGNUMBER", strings.Join(catnos, MultiSep)},
			{"MEDIA", medium.Format},
			{"SCRIPT", r.TextRepresentation.Script},
			{"ISRC", isrc},
			{"originaldate", original},
			{"originalyear", left(original, 4)},
			{"albumgrouping", g.Grouping},
			{"albumgenre", strings.Join(g.Genres, MultiSep)},
			{"albummood", strings.Join(g.Moods, MultiSep)},
			{"MOOD", strings.Join(g.Moods, MultiSep)},
		},
	}
	if src.ReplayGain != 0 {
		t.Freeform = append(t.Freeform, Field{"replaygain_track_gain", fmt.Sprintf("%.2f dB", src.ReplayGain)})
	}
	if src.Peak != 0 {
		t.Freeform = append(t.Freeform, Field{"replaygain_track_peak", fmt.Sprintf("%.6f", src.Peak)})
	}
	t.asciiPunct()
	return t, nil
}

// locate finds the i-th track (0-based) across the release's media, returning
// its disc number and medium.
func locate(r musicbrainz.Release, i int) (disc int, medium musicbrainz.Medium, track musicbrainz.Track, ok bool) {
	for n, m := range r.Media {
		if i < len(m.Tracks) {
			disc = m.Position
			if disc == 0 {
				disc = n + 1
			}
			return disc, m, m.Tracks[i], true
		}
		i -= len(m.Tracks)
	}
	return 0, musicbrainz.Medium{}, musicbrainz.Track{}, false
}

func left(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
