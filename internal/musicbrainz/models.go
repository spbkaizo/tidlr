package musicbrainz

import "strings"

// Release is a MusicBrainz release as returned by a lookup with releaseInc
// (search hits carry a subset: no recordings).
type Release struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Status             string       `json:"status"`
	Country            string       `json:"country"`
	Date               string       `json:"date"`
	Barcode            string       `json:"barcode"`
	ArtistCredit       []Credit     `json:"artist-credit"`
	LabelInfo          []LabelInfo  `json:"label-info"`
	ReleaseGroup       ReleaseGroup `json:"release-group"`
	Media              []Medium     `json:"media"`
	TrackCount         int          `json:"track-count"` // search hits only
	TextRepresentation TextRepr     `json:"text-representation"`
	Genres             []Genre      `json:"genres"`
	Score              int          `json:"score"` // search hits only
}

// Credit is one artist in an artist credit, with the phrase joining it to the
// next ("Kendrick Lamar", " feat. ").
type Credit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	} `json:"artist"`
}

// LabelInfo is a release's label and catalogue number.
type LabelInfo struct {
	CatalogNumber string `json:"catalog-number"`
	Label         struct {
		Name string `json:"name"`
	} `json:"label"`
}

// ReleaseGroup groups every release of the same album.
type ReleaseGroup struct {
	ID               string   `json:"id"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
	Genres           []Genre  `json:"genres"`
}

// Medium is one disc (or other medium) of a release.
type Medium struct {
	Position   int     `json:"position"`
	Format     string  `json:"format"`
	TrackCount int     `json:"track-count"`
	Tracks     []Track `json:"tracks"`
}

// Track is a track on a medium.
type Track struct {
	ID           string    `json:"id"`
	Position     int       `json:"position"`
	Title        string    `json:"title"`
	Length       int       `json:"length"` // milliseconds; 0 if unknown
	ArtistCredit []Credit  `json:"artist-credit"`
	Recording    Recording `json:"recording"`
}

// Recording is the underlying recording of a track.
type Recording struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Length int      `json:"length"`
	ISRCs  []string `json:"isrcs"`
}

// TextRepr is the language and script of a release's titles.
type TextRepr struct {
	Language string `json:"language"`
	Script   string `json:"script"`
}

// Genre is a MusicBrainz genre with its vote count.
type Genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// CreditName joins an artist credit as it is displayed ("A feat. B & C").
func CreditName(cs []Credit) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Name)
		b.WriteString(c.JoinPhrase)
	}
	return b.String()
}

// CreditSortName joins an artist credit using each artist's sort name, the
// way Picard builds artistsort ("Lamar, Kendrick feat. Clinton, George").
func CreditSortName(cs []Credit) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Artist.SortName)
		b.WriteString(c.JoinPhrase)
	}
	return b.String()
}

// CreditIDs returns the artist ids of a credit, in order.
func CreditIDs(cs []Credit) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.Artist.ID
	}
	return ids
}

// CreditArtists returns each credited artist's own name (Picard's ARTISTS).
func CreditArtists(cs []Credit) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Artist.Name
	}
	return names
}

// ReleaseTypes is the release group's primary and secondary types in lower
// case, as Picard writes them ("album", "soundtrack").
func (r Release) ReleaseTypes() []string {
	var types []string
	if r.ReleaseGroup.PrimaryType != "" {
		types = append(types, strings.ToLower(r.ReleaseGroup.PrimaryType))
	}
	for _, t := range r.ReleaseGroup.SecondaryTypes {
		types = append(types, strings.ToLower(t))
	}
	return types
}

// Tracks flattens the release's tracks across media, in disc then track order.
func (r Release) Tracks() []Track {
	var out []Track
	for _, m := range r.Media {
		out = append(out, m.Tracks...)
	}
	return out
}

// TotalTracks counts tracks across all media.
func (r Release) TotalTracks() int {
	n := 0
	for _, m := range r.Media {
		if len(m.Tracks) > 0 {
			n += len(m.Tracks)
		} else {
			n += m.TrackCount
		}
	}
	if n == 0 {
		return r.TrackCount
	}
	return n
}
