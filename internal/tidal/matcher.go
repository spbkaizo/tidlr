package tidal

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
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
	Lossless     bool // true => downloadable as lossless FLAC
	Score        float64
	FromSearchID int64 // the album search returned before edition upgrade
	Upgraded     bool  // true if we swapped to a better edition than search's hit
}

const (
	// minScore is the blended artist+title similarity required to accept a hit.
	minScore = 0.62
	// minArtistSim is the artist similarity a hit needs on its own, whatever
	// its title scores.
	minArtistSim = 0.5
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

// sideReleaseRe matches a bracketed tag marking something other than the
// album itself: an instrumental or a cappella version, an interview, an edit,
// remixes or a live set. Tidal files these under the album's own title, and
// normalize strips the tag, so without this they pass for the album.
// "Edition" is not "edit": the word boundary keeps deluxe editions.
var sideReleaseRe = regexp.MustCompile(`(?i)[\(\[][^\)\]]*\b(instrumentals?|a ?cappellas?|interviews?|commentary|karaoke|edit|remix(?:es)?|live|demos?)\b[^\)\]]*[\)\]]`)

// acceptable reports whether a can stand for wantTitle at all: not a side
// release (unless one was asked for), the same sequel number ("Run the
// Jewels" is not "Run the Jewels 3"), and already released. A pre-release
// edition lists every track but serves only the few already out.
func acceptable(a Album, wantTitle string, now time.Time) bool {
	if m := sideReleaseRe.FindStringSubmatch(a.Title); m != nil &&
		!strings.Contains(strings.ToLower(wantTitle), strings.ToLower(m[1])) {
		return false
	}
	if TitleNumbers(normalize(a.Title)) != TitleNumbers(normalize(wantTitle)) {
		return false
	}
	return !a.ReleaseTime().After(now)
}

// Normalize is the exported form of normalize.
func Normalize(s string) string { return normalize(s) }

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

// sameArtist reports whether any of a's artists can be wantArtist: similar
// enough, or one name containing the other ("Anohni" in "ANOHNI and the
// Johnsons"). Without it an exact title by someone else outscores minScore on
// title alone (Pale Honey's "Devotion" for Jessie Ware's).
func sameArtist(a Album, wantArtist string) bool {
	return anyArtist(append([]Artist{a.Artist}, a.Artists...), wantArtist)
}

func anyArtist(artists []Artist, wantArtist string) bool {
	want := normalize(wantArtist)
	for _, ar := range artists {
		got := normalize(ar.Name)
		if got == "" {
			continue
		}
		if similarityRatio(got, want) >= minArtistSim ||
			strings.Contains(" "+got+" ", " "+want+" ") || strings.Contains(" "+want+" ", " "+got+" ") {
			return true
		}
	}
	return false
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
	var matched *Album
	var best float64
	now := time.Now()
	for _, query := range searchQueries(wantArtist, wantTitle) {
		res, err := m.Client.Search(ctx, query)
		if err != nil {
			return Match{}, err
		}
		for i := range res.Albums.Items {
			if !acceptable(res.Albums.Items[i], wantTitle, now) || !sameArtist(res.Albums.Items[i], wantArtist) {
				continue
			}
			s := nameScore(res.Albums.Items[i], wantArtist, wantTitle)
			if matched == nil || s > best {
				matched = &res.Albums.Items[i]
				best = s
			}
		}
		if matched != nil && best >= minScore {
			break
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

// shortTitleWords is how many title words the fallback search keeps.
const shortTitleWords = 4

// searchQueries returns the queries to try in turn: "artist title", then, for a
// long title, "artist" plus its first few words. Tidal's search returns nothing
// at all for a very long query (Fiona Apple's 23-word "The Idler Wheel…"),
// while a few words find it; candidates are still scored against the full
// title, so the shorter query cannot loosen the match.
func searchQueries(artist, title string) []string {
	queries := []string{artist + " " + title}
	if words := strings.Fields(title); len(words) > shortTitleWords {
		queries = append(queries, artist+" "+strings.Join(words[:shortTitleWords], " "))
	}
	return queries
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

	now := time.Now()
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
			if !acceptable(al, wantTitle, now) {
				continue
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

// WantTrack is a track an album lacks on Tidal, as MusicBrainz lists it.
type WantTrack struct {
	Artist, Title string
	Seconds       int // 0 if unknown
	ISRCs         []string
}

// maxTrackDrift is how far a stand-in track's length may be from the wanted
// one when no ISRC vouches for it.
const maxTrackDrift = 3

// FindTrack looks for want elsewhere on Tidal: a single, a compilation, or
// the original of a track an album borrows. A hit with one of want's ISRCs is
// the same recording; otherwise it needs the same artist, the same title and
// a length within maxTrackDrift. Returns ErrNoMatch if nothing qualifies.
func (m *Matcher) FindTrack(ctx context.Context, want WantTrack) (Track, error) {
	var fallback *Track
	for _, query := range []string{want.Artist + " " + want.Title, want.Title} {
		res, err := m.Client.Search(ctx, query)
		if err != nil {
			return Track{}, err
		}
		for i, tr := range res.Tracks.Items {
			if slices.Contains(want.ISRCs, tr.ISRC) {
				return tr, nil
			}
			if fallback == nil && sameTrack(tr, want) {
				fallback = &res.Tracks.Items[i]
			}
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return Track{}, ErrNoMatch
}

// sameTrack reports whether tr has want's artist, title and length, and is
// not a side version (live, remix, edit) of it.
func sameTrack(tr Track, want WantTrack) bool {
	if want.Seconds == 0 || tr.Duration-want.Seconds > maxTrackDrift || want.Seconds-tr.Duration > maxTrackDrift {
		return false
	}
	if m := sideReleaseRe.FindStringSubmatch(tr.Title); m != nil &&
		!strings.Contains(strings.ToLower(want.Title), strings.ToLower(m[1])) {
		return false
	}
	return similarityRatio(normalize(tr.Title), normalize(want.Title)) >= 0.9 &&
		anyArtist(append([]Artist{tr.Artist}, tr.Artists...), want.Artist)
}
