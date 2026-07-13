package tidal

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// ErrNoMatch means no Tidal album confidently matched the requested name.
var ErrNoMatch = errors.New("no confident Tidal match")

// Match is the resolved album chosen for a requested artist/title.
type Match struct {
	ID           int64
	Title        string
	Artist       string
	Mode         string // primary audioMode (STEREO, DOLBY_ATMOS, ...)
	Explicit     bool
	Lossless     bool  // true => downloadable as lossless FLAC
	Score        float64
	FromSearchID int64 // the album search returned before edition upgrade
	Upgraded     bool  // true if we swapped to a better edition than search's hit
}

const (
	// minScore is the blended artist+title similarity required to accept a hit.
	minScore = 0.62
	// sameAlbumTitleSim is how close a catalogue album's title must be to count
	// as another edition of the matched album.
	sameAlbumTitleSim = 0.80
	// maxArtistAlbums bounds how deep we page an artist's catalogue.
	maxArtistAlbums = 100
)

var bracketRe = regexp.MustCompile(`[\(\[].*?[\)\]]`)
var nonAlnumRe = regexp.MustCompile(`[^a-z0-9 ]+`)
var wsRe = regexp.MustCompile(`\s+`)

// normalize lowercases and strips bracketed edition tags and punctuation so
// titles compare on their core name ("Roses (Deluxe) [STEREO]" -> "roses").
func normalize(s string) string {
	s = strings.ToLower(s)
	s = bracketRe.ReplaceAllString(s, " ")
	s = nonAlnumRe.ReplaceAllString(s, " ")
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// isLossless reports whether the album is downloadable as lossless FLAC: it must
// carry a STEREO mode (Atmos-only comes as lossy EAC3) and a lossless quality.
func isLossless(a Album) bool {
	if !a.HasMode("STEREO") {
		return false
	}
	switch strings.ToUpper(a.AudioQuality) {
	case "LOSSLESS", "HI_RES", "HI_RES_LOSSLESS", "":
		return true
	default:
		return false
	}
}

// nameScore blends title (0.6) and artist (0.4) similarity in [0,1].
func nameScore(a Album, wantArtist, wantTitle string) float64 {
	titleSim := similarityRatio(normalize(a.Title), normalize(wantTitle))
	artistSim := similarityRatio(normalize(a.ArtistName()), normalize(wantArtist))
	return titleSim*0.6 + artistSim*0.4
}

// editionKey ranks editions of the same album: lossless > Atmos-only, then
// explicit (uncensored original) > clean, then newer release. Compared
// field-by-field, higher wins.
type editionKey struct {
	lossless int
	explicit int
	release  int64
}

func editionRank(a Album) editionKey {
	k := editionKey{}
	if isLossless(a) {
		k.lossless = 1
	}
	if a.Explicit {
		k.explicit = 1
	}
	k.release = a.ReleaseTime().Unix()
	return k
}

// better reports whether x ranks above y.
func (x editionKey) better(y editionKey) bool {
	if x.lossless != y.lossless {
		return x.lossless > y.lossless
	}
	if x.explicit != y.explicit {
		return x.explicit > y.explicit
	}
	return x.release > y.release
}

// Matcher resolves ADM artist/title names to the best Tidal album.
type Matcher struct {
	Client *Client
}

// Match runs the two-stage lookup: (1) search to find the best artist+title
// hit, then (2) enumerate that artist's catalogue to upgrade to the best
// edition of the same album — surfacing lossless STEREO editions that Tidal's
// search endpoint frequently hides behind a Dolby Atmos edition. Returns
// ErrNoMatch when nothing clears the confidence bar.
func (m *Matcher) Match(ctx context.Context, wantArtist, wantTitle string) (Match, error) {
	res, err := m.Client.Search(ctx, wantArtist+" "+wantTitle)
	if err != nil {
		return Match{}, err
	}

	var matched *Album
	var best float64
	for i := range res.Albums.Items {
		s := nameScore(res.Albums.Items[i], wantArtist, wantTitle)
		if matched == nil || s > best {
			matched = &res.Albums.Items[i]
			best = s
		}
	}
	if matched == nil || best < minScore {
		return Match{}, ErrNoMatch
	}

	chosen := m.bestEdition(ctx, *matched, wantTitle)

	return Match{
		ID:           chosen.ID,
		Title:        chosen.Title,
		Artist:       chosen.ArtistName(),
		Mode:         chosen.PrimaryMode(),
		Explicit:     chosen.Explicit,
		Lossless:     isLossless(chosen),
		Score:        best,
		FromSearchID: matched.ID,
		Upgraded:     chosen.ID != matched.ID,
	}, nil
}

// bestEdition enumerates the matched album's artist catalogue and returns the
// best edition of the same album. Best-effort: on any API error it returns the
// original matched album unchanged.
func (m *Matcher) bestEdition(ctx context.Context, matched Album, wantTitle string) Album {
	aid := matched.ArtistID()
	if aid == 0 {
		return matched
	}
	wantNorm := normalize(wantTitle)
	matchedNorm := normalize(matched.Title)

	best := matched
	bestKey := editionRank(matched)
	seen := map[int64]bool{matched.ID: true}

	for offset := 0; offset < maxArtistAlbums; offset += 10 {
		page, err := m.Client.GetArtistAlbums(ctx, aid, 10, offset)
		if err != nil || len(page.Items) == 0 {
			break
		}
		for _, al := range page.Items {
			if seen[al.ID] {
				continue
			}
			seen[al.ID] = true
			t := normalize(al.Title)
			if similarityRatio(t, wantNorm) < sameAlbumTitleSim &&
				similarityRatio(t, matchedNorm) < sameAlbumTitleSim {
				continue // different album
			}
			if k := editionRank(al); k.better(bestKey) {
				best, bestKey = al, k
			}
		}
		if offset+10 >= page.TotalNumberOfItems {
			break
		}
	}
	return best
}
