package genre

import (
	"sort"
	"strings"
)

// Tag is a tag name with its score.
type Tag struct {
	Name  string
	Score float64
}

// Result is an album's categorized tags, title-cased. Empty fields mean no tag
// qualified.
type Result struct {
	Grouping string
	Genres   []string
	Moods    []string
}

// Weights for merging an album's toptag sources, as in lastfmng: the album's
// own tags count weightAlbum per track (to balance the per-track sources),
// every track's artist tags weightArtist, every track's tags weightTrack.
const (
	weightAlbum  = 15
	weightArtist = 55
	weightTrack  = 30
)

// Merge combines weighted tag lists into one, summing scores of the same tag,
// best first (ties broken by name for stable output).
func Merge(lists ...[]Tag) []Tag {
	sum := map[string]float64{}
	for _, l := range lists {
		for _, t := range l {
			sum[t.Name] += t.Score
		}
	}
	out := make([]Tag, 0, len(sum))
	for n, s := range sum {
		out = append(out, Tag{n, s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// weigh multiplies every score in tags by w.
func weigh(tags []Tag, w float64) []Tag {
	out := make([]Tag, len(tags))
	for i, t := range tags {
		out[i] = Tag{t.Name, t.Score * w}
	}
	return out
}

// Categorize sorts merged tags (best first) into grouping, genre and mood,
// following lastfmng: each category keeps whitelisted tags scoring at least
// its threshold times the best of them, up to its limit; grouping's extra
// tags overflow to the front of genre.
func (l Lists) Categorize(tags []Tag) Result {
	result := map[string][]Tag{}
	for _, c := range []Category{l.Grouping, l.Genre, l.Mood} {
		var kept []Tag
		for _, t := range tags {
			if c.has(t.Name) {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			floor := kept[0].Score * c.Threshold
			n := 0
			for _, t := range kept {
				if t.Score >= floor {
					kept[n] = t
					n++
				}
			}
			kept = kept[:n]
		}
		limit := c.Limit
		if limit <= 0 || limit > len(kept) {
			limit = len(kept)
		}
		result[c.Name] = append(result[c.Name], kept[:limit]...)
		if c.Overflow != "" && len(kept) > limit {
			result[c.Overflow] = kept[limit:]
		}
	}
	names := func(c Category) []string {
		// A tag in two whitelists can arrive both by overflow and directly
		// ("rap" is a grouping and a genre); list it once.
		seen := map[string]bool{}
		out := []string{}
		for _, t := range result[c.Name] {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			out = append(out, l.titleCase(t.Name))
		}
		if c.Limit > 0 && len(out) > c.Limit {
			out = out[:c.Limit]
		}
		return out
	}
	r := Result{Genres: names(l.Genre), Moods: names(l.Mood)}
	if g := names(l.Grouping); len(g) > 0 {
		r.Grouping = g[0]
	}
	return r
}

// translate maps a raw tag to lastfmng's canonical name: lower case, trimmed,
// then through the translations table.
func (l Lists) translate(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if to, ok := l.Translations[name]; ok {
		return to
	}
	return name
}

// smallWords stay lower case inside a title, as python-titlecase (used by
// lastfmng) leaves them.
var smallWords = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "but": true, "by": true,
	"en": true, "for": true, "if": true, "in": true, "of": true, "on": true, "or": true,
	"the": true, "to": true, "v": true, "v.": true, "via": true, "vs": true, "vs.": true,
}

// titleCase capitalizes each word and each hyphenated part ("hip-hop" ->
// "Hip-Hop", "drum & bass" -> "Drum & Bass", "r&b" -> "R&b"), keeping small
// words lower case after the first and abbreviations upper case.
func (l Lists) titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if l.Abbreviations[strings.ToUpper(w)] {
			words[i] = strings.ToUpper(w)
			continue
		}
		if i > 0 && smallWords[w] {
			continue
		}
		parts := strings.Split(w, "-")
		for j, p := range parts {
			if p != "" {
				parts[j] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}
