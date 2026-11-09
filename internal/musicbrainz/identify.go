package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	maxShift        = 4000  // the largest whole-album offset taken out first
)

// Identify finds the MusicBrainz release for want: by barcode, then by the
// releases containing its first ISRCs, then by artist and title search. Each
// stage's candidates are fetched in full and kept only if their track list
// fits want (same count, lengths within tolerance); the first stage to yield a
// fit decides, and prefs choose among its fits. If none fits, every other
// release of the albums those stages found is tried: another pressing or
// edition may carry the tracks Tidal has.
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
	var groups []string
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
			if g := r.ReleaseGroup.ID; g != "" && !slices.Contains(groups, g) && sameAlbum(r, want) {
				groups = append(groups, g)
			}
		}
		if len(fits) > 0 {
			return Match{Release: Best(fits, want, prefs), Method: s.method}, nil
		}
	}
	more, err := c.groupCandidates(ctx, want)
	if err != nil {
		return Match{}, fmt.Errorf("release group lookup: %w", err)
	}
	for _, g := range more {
		if !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	return c.groupMatch(ctx, want, prefs, groups, tried)
}

// groupCandidates finds the album's release groups by artist and core title,
// whatever the track count: the edition Tidal has may not be among the
// releases the earlier stages found.
func (c *Client) groupCandidates(ctx context.Context, want Want) ([]string, error) {
	hits, err := c.SearchReleases(ctx, fmt.Sprintf("artist:%s AND release:%s", quote(want.Artist), quote(coreTitle(want.Title))), 25)
	if err != nil {
		return nil, err
	}
	var groups []string
	for _, h := range hits {
		if g := h.ReleaseGroup.ID; g != "" && !slices.Contains(groups, g) && sameAlbum(h, want) {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// editionRe matches an edition named after a dash or colon: "Devotion - The
// Gold Edition", "Hadestown: Deluxe".
var editionRe = regexp.MustCompile(`(?i)\s*[-:–]\s+[^-:–]*\b(edition|deluxe|expanded|remaster(ed)?|anniversary|version|reissue)\b.*$`)

// coreTitle is title without its edition: "Devotion - The Gold Edition" and
// "{Awayland} (Digital Deluxe)" are the albums "Devotion" and "{Awayland}".
func coreTitle(title string) string {
	return strings.TrimSpace(bracketRe.ReplaceAllString(editionRe.ReplaceAllString(title, ""), " "))
}

var bracketRe = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)

// maxGroups caps how many release groups the last stage browses.
const maxGroups = 2

// groupMatch tries every release in groups not already tried, and fetches the
// best fit in full.
func (c *Client) groupMatch(ctx context.Context, want Want, prefs Prefs, groups []string, tried map[string]bool) (Match, error) {
	var fits []Release
	for _, g := range firstN(groups, maxGroups) {
		rs, err := c.ReleasesInGroup(ctx, g)
		if err != nil {
			return Match{}, fmt.Errorf("release group %s: %w", g, err)
		}
		for _, r := range rs {
			if !tried[r.ID] && Fits(r, want) {
				fits = append(fits, r)
			}
		}
	}
	if len(fits) == 0 {
		return Match{}, ErrNoMatch
	}
	id := Best(fits, want, prefs).ID
	r, err := c.LookupRelease(ctx, id)
	if err != nil {
		return Match{}, fmt.Errorf("release %s: %w", id, err)
	}
	return Match{Release: r, Method: "release group"}, nil
}

// sameAlbum reports whether r's artist and title are close to want's, the
// title with or without its edition.
func sameAlbum(r Release, want Want) bool {
	if tidal.Similarity(tidal.Normalize(CreditName(r.ArtistCredit)), tidal.Normalize(want.Artist)) < 0.8 {
		return false
	}
	got := tidal.Normalize(coreTitle(r.Title))
	return tidal.Similarity(got, tidal.Normalize(want.Title)) >= 0.8 ||
		tidal.Similarity(got, tidal.Normalize(coreTitle(want.Title))) >= 0.8
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
	// MusicBrainz usually holds the 13-digit EAN (a UPC-A with a leading
	// zero); Tidal pads to 12 or 14 digits.
	if t := strings.TrimLeft(code, "0"); t != "" && len(t) < 13 {
		if ean := strings.Repeat("0", 13-len(t)) + t; !slices.Contains(forms, ean) {
			forms = append(forms, ean)
		}
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
// defeat the phrase query ("D’Angelo" with a curly apostrophe). A title
// numbered in digits is also tried in Roman numerals ("Angels of Darkness,
// Demons of Light I").
func (c *Client) searchCandidates(ctx context.Context, want Want) ([]string, error) {
	titles := []string{want.Title}
	if r := romanNumerals(want.Title); r != want.Title {
		titles = append(titles, r)
	}
	var queries []string
	for _, t := range titles {
		queries = append(queries,
			fmt.Sprintf("artist:%s AND release:%s AND tracks:%d", quote(want.Artist), quote(t), len(want.Tracks)),
			fmt.Sprintf("release:%s AND tracks:%d", quote(t), len(want.Tracks)))
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

var numberRe = regexp.MustCompile(`\b([1-9]|10)\b`)

// romanNumerals writes the numbers 1 to 10 in title as Roman numerals.
func romanNumerals(title string) string {
	roman := []string{"", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}
	return numberRe.ReplaceAllStringFunc(title, func(n string) string {
		i, _ := strconv.Atoi(n)
		return roman[i]
	})
}

// Fits reports whether r's track list matches want: the same number of
// tracks, and either the same recordings (every ISRC both sides know agrees,
// across most of the album) or lengths that agree. Lengths are compared after
// removing the album's typical offset (a master with longer gaps runs every
// track a couple of seconds long), and a pair of neighbours that are out by
// opposite amounts counts as one moved track boundary. Then each track must be
// within lengthTolerance, bar one in eight (none for under four tracks)
// within maxOutlier, with the mean difference under maxMeanDiff.
func Fits(r Release, want Want) bool {
	tracks := r.Tracks()
	if len(tracks) == 0 || len(tracks) != len(want.Tracks) {
		return false
	}
	if sameRecordings(tracks, want) {
		return true
	}
	diffs := make([]int, len(tracks))
	known := make([]bool, len(tracks))
	var signed []int
	for i, t := range tracks {
		ms := t.Length
		if ms == 0 {
			ms = t.Recording.Length
		}
		w := want.Tracks[i].Seconds * 1000
		if ms == 0 || w == 0 {
			continue
		}
		diffs[i], known[i] = ms-w, true
		signed = append(signed, ms-w)
	}
	if len(signed) == 0 {
		return true
	}
	if shift := median(signed); len(signed) >= 4 && abs(shift) <= maxShift {
		for i := range diffs {
			diffs[i] -= shift
		}
	}

	var compared, total, outliers int
	for i := 0; i < len(diffs); i++ {
		if !known[i] {
			continue
		}
		d := abs(diffs[i])
		// A boundary moved between neighbours: one long, the next short by
		// about as much. Together they agree.
		if d > lengthTolerance && i+1 < len(diffs) && known[i+1] &&
			(diffs[i] > 0) != (diffs[i+1] > 0) && abs(diffs[i]+diffs[i+1]) <= lengthTolerance {
			pair := abs(diffs[i] + diffs[i+1])
			compared += 2
			total += pair
			i++
			continue
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
	// One outlier per eight tracks; a single or EP of under four, none.
	allowed := len(tracks) / 8
	if len(tracks) >= 4 {
		allowed = max(allowed, 1)
	}
	return outliers <= allowed && total/compared < maxMeanDiff
}

// sameRecordings reports whether ISRCs prove r's tracks are want's: both
// sides know the ISRC for at least three tracks and three in four, and every
// one of those agrees. Lengths then only reflect how each catalogue measured
// them (a hidden track cut, a fade trimmed).
func sameRecordings(tracks []Track, want Want) bool {
	compared := 0
	for i, t := range tracks {
		w := want.Tracks[i].ISRC
		if w == "" || len(t.Recording.ISRCs) == 0 {
			continue
		}
		if !slices.Contains(t.Recording.ISRCs, w) {
			return false
		}
		compared++
	}
	return compared >= 3 && compared*4 >= len(tracks)*3
}

func median(xs []int) int {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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
