package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/simonb/tidlr/internal/tidal"
)

// ErrNoMatch means no MusicBrainz release fits the downloaded album.
var ErrNoMatch = errors.New("no matching MusicBrainz release")

// Want describes a downloaded album to identify.
type Want struct {
	Artist  string
	Title   string
	Barcode string
	Tracks  []WantTrack // in disc, then track order
}

// WantTrack is one downloaded track.
type WantTrack struct {
	Seconds int    // duration; 0 if unknown
	ISRC    string // "" if unknown
}

// Prefs orders acceptable releases, mirroring Picard's preferred release
// countries and formats. Earlier entries win.
type Prefs struct {
	Countries []string
	Formats   []string
}

// DefaultPrefs prefers UK, then worldwide, then US releases, and digital
// over CD, as Picard's preferred release countries and formats would.
var DefaultPrefs = Prefs{
	Countries: []string{"GB", "XW", "US"},
	Formats:   []string{"Digital Media", "CD"},
}

// Match is an identified release and how it was found.
type Match struct {
	Release Release
	Method  string // "barcode", "isrc" or "search"
}

// maxCandidates caps release lookups per album: each costs a second under the
// rate limit.
const maxCandidates = 8

// Track lengths are compared in milliseconds. Tidal reports whole seconds,
// and a reissue or another master often moves one track by a few seconds (a
// longer fade, a hidden track), so one outlier in eight may differ by up to
// maxOutlier as long as the album as a whole agrees closely.
const (
	lengthTolerance = 3000  // a track within this agrees
	maxOutlier      = 10000 // no track may differ by more
	maxMeanDiff     = 2000  // the average difference across the album
)

// Identify finds the MusicBrainz release for want: by barcode, then by the
// releases containing its first ISRCs, then by artist and title search. Each
// stage's candidates are fetched in full and kept only if their track list
// fits want (same count, lengths within tolerance); the first stage to yield a
// fit decides, and prefs choose among its fits.
func (c *Client) Identify(ctx context.Context, want Want, prefs Prefs) (Match, error) {
	stages := []struct {
		method string
		ids    func() ([]string, error)
	}{
		{"barcode", func() ([]string, error) { return c.barcodeCandidates(ctx, want) }},
		{"isrc", func() ([]string, error) { return c.isrcCandidates(ctx, want) }},
		{"search", func() ([]string, error) { return c.searchCandidates(ctx, want) }},
	}
	tried := map[string]bool{}
	for _, s := range stages {
		ids, err := s.ids()
		if err != nil {
			return Match{}, fmt.Errorf("%s lookup: %w", s.method, err)
		}
		var fits []Release
		for _, id := range ids {
			if tried[id] || len(tried) >= maxCandidates*2 {
				continue
			}
			tried[id] = true
			r, err := c.LookupRelease(ctx, id)
			if err != nil {
				return Match{}, fmt.Errorf("release %s: %w", id, err)
			}
			if Fits(r, want) {
				fits = append(fits, r)
			}
		}
		if len(fits) > 0 {
			return Match{Release: Best(fits, want, prefs), Method: s.method}, nil
		}
	}
	return Match{}, ErrNoMatch
}

// barcodeCandidates searches by UPC. Tidal pads UPCs with leading zeros that
// the printed barcode (as entered on MusicBrainz) usually lacks, so both forms
// are tried.
func (c *Client) barcodeCandidates(ctx context.Context, want Want) ([]string, error) {
	code := strings.TrimSpace(want.Barcode)
	if code == "" {
		return nil, nil
	}
	forms := []string{code}
	if t := strings.TrimLeft(code, "0"); t != code && t != "" {
		forms = append(forms, t)
	}
	if len(code) == 12 { // UPC-A is EAN-13 with a leading zero
		forms = append(forms, "0"+code)
	}
	var terms []string
	for _, f := range forms {
		terms = append(terms, "barcode:"+f)
	}
	hits, err := c.SearchReleases(ctx, strings.Join(terms, " OR "), 25)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range hits {
		if slices.Contains(forms, h.Barcode) {
			ids = append(ids, h.ID)
		}
	}
	return firstN(ids, maxCandidates), nil
}

// isrcCandidates returns releases containing both of the first two ISRCs (or
// the first, if only one is known), most specific first.
func (c *Client) isrcCandidates(ctx context.Context, want Want) ([]string, error) {
	var isrcs []string
	for _, t := range want.Tracks {
		if t.ISRC != "" {
			isrcs = append(isrcs, t.ISRC)
		}
		if len(isrcs) == 2 {
			break
		}
	}
	if len(isrcs) == 0 {
		return nil, nil
	}
	ids, err := c.ReleaseIDsByISRC(ctx, isrcs[0])
	if err != nil || len(isrcs) == 1 {
		return firstN(dedupe(ids), maxCandidates), err
	}
	second, err := c.ReleaseIDsByISRC(ctx, isrcs[1])
	if err != nil {
		return nil, err
	}
	var both []string
	for _, id := range dedupe(ids) {
		if slices.Contains(second, id) {
			both = append(both, id)
		}
	}
	return firstN(both, maxCandidates), nil
}

// searchCandidates searches by artist, title and track count, keeping hits
// whose names are close to want's. If that finds nothing it searches by
// title and track count alone: MusicBrainz's spelling of the artist can
// defeat the phrase query ("D’Angelo" with a curly apostrophe).
func (c *Client) searchCandidates(ctx context.Context, want Want) ([]string, error) {
	queries := []string{
		fmt.Sprintf("artist:%s AND release:%s AND tracks:%d", quote(want.Artist), quote(want.Title), len(want.Tracks)),
		fmt.Sprintf("release:%s AND tracks:%d", quote(want.Title), len(want.Tracks)),
	}
	wantArtist, wantTitle := tidal.Normalize(want.Artist), tidal.Normalize(want.Title)
	for _, q := range queries {
		hits, err := c.SearchReleases(ctx, q, 25)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, h := range hits {
			if tidal.Similarity(tidal.Normalize(CreditName(h.ArtistCredit)), wantArtist) < 0.8 ||
				tidal.Similarity(tidal.Normalize(h.Title), wantTitle) < 0.8 {
				continue
			}
			ids = append(ids, h.ID)
		}
		if len(ids) > 0 {
			return firstN(ids, maxCandidates), nil
		}
	}
	return nil, nil
}

// Fits reports whether r's track list matches want: the same number of
// tracks, and lengths (where both sides know them) that agree: each within
// lengthTolerance, bar one track in eight (none for under four tracks)
// within maxOutlier, with the mean difference under maxMeanDiff.
func Fits(r Release, want Want) bool {
	tracks := r.Tracks()
	if len(tracks) == 0 || len(tracks) != len(want.Tracks) {
		return false
	}
	var compared, total, outliers int
	for i, t := range tracks {
		ms := t.Length
		if ms == 0 {
			ms = t.Recording.Length
		}
		w := want.Tracks[i].Seconds * 1000
		if ms == 0 || w == 0 {
			continue
		}
		d := ms - w
		if d < 0 {
			d = -d
		}
		if d > maxOutlier {
			return false
		}
		if d > lengthTolerance {
			outliers++
		}
		compared++
		total += d
	}
	if compared == 0 {
		return true
	}
	// One outlier per eight tracks; a single or EP of under four, none.
	allowed := len(tracks) / 8
	if len(tracks) >= 4 {
		allowed = max(allowed, 1)
	}
	return outliers <= allowed && total/compared < maxMeanDiff
}

// Best picks among fitting releases: most matching ISRCs first, then
// official status, preferred country, preferred format, then the earliest
// release date (the original over later reissues).
func Best(fits []Release, want Want, prefs Prefs) Release {
	type key struct {
		isrcs, official, country, format int
		date                             string
	}
	score := func(r Release) key {
		k := key{date: r.Date}
		if k.date == "" {
			k.date = "9999"
		}
		for i, t := range r.Tracks() {
			if i < len(want.Tracks) && want.Tracks[i].ISRC != "" && slices.Contains(t.Recording.ISRCs, want.Tracks[i].ISRC) {
				k.isrcs++
			}
		}
		if strings.EqualFold(r.Status, "official") {
			k.official = 1
		}
		k.country = rank(prefs.Countries, r.Country)
		if len(r.Media) > 0 {
			k.format = rank(prefs.Formats, r.Media[0].Format)
		}
		return k
	}
	better := func(a, b key) bool {
		switch {
		case a.isrcs != b.isrcs:
			return a.isrcs > b.isrcs
		case a.official != b.official:
			return a.official > b.official
		case a.country != b.country:
			return a.country > b.country
		case a.format != b.format:
			return a.format > b.format
		default:
			return a.date < b.date
		}
	}
	best, bestKey := fits[0], score(fits[0])
	for _, r := range fits[1:] {
		if k := score(r); better(k, bestKey) {
			best, bestKey = r, k
		}
	}
	return best
}

// rank scores v by its position in prefs: the first preference scores highest,
// anything unlisted scores 0.
func rank(prefs []string, v string) int {
	for i, p := range prefs {
		if strings.EqualFold(p, v) {
			return len(prefs) - i
		}
	}
	return 0
}

// quote makes s a Lucene phrase, escaping the characters that would end or
// alter it.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func firstN(ids []string, n int) []string {
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}
