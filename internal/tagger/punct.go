package tagger

import "strings"

// punct maps Unicode punctuation to ASCII, as Picard's convert_punctuation
// option does to every tag it writes ("I’m Scum" -> "I'm Scum").
var punct = strings.NewReplacer(
	"‘", "'", "’", "'", "‚", "'", "‛", "'", "′", "'",
	"“", `"`, "”", `"`, "„", `"`, "‟", `"`, "″", `"`,
	"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-", "―", "-", "−", "-",
	"…", "...",
	"⁄", "/", "∕", "/",
	"‹", "<", "›", ">", "«", "<<", "»", ">>",
)

// ASCIIPunct converts Unicode punctuation in s to its ASCII equivalent.
func ASCIIPunct(s string) string { return punct.Replace(s) }

// asciiPunct applies ASCIIPunct to every text value of t.
func (t *Tags) asciiPunct() {
	for _, p := range []*string{&t.Title, &t.Artist, &t.AlbumArtist, &t.Album, &t.Genre, &t.Grouping,
		&t.Copyright, &t.SortArtist, &t.SortAlbumArtist} {
		*p = ASCIIPunct(*p)
	}
	for i := range t.Freeform {
		t.Freeform[i].Value = ASCIIPunct(t.Freeform[i].Value)
	}
}
