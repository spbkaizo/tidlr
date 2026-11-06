// Package filing decides where a tagged album lives in the library and moves
// it there. The layout ports the user's Picard file naming script:
//
//	<sub>/<albumgrouping|Unknown>/<albumartist> - <year|0000> - <album>/<disc><NN> - <title>
//
// where <sub> is "Archive Soundtracks" for a soundtrack, "Archive
// Compilations" for a Various Artists compilation, else "Archive Albums".
// Picard's own path clean-up (windows compatibility, "/" in values, leading
// and trailing dots) is reproduced too, so tidlr files albums exactly where
// Picard would have.
package filing

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/simonb/tidlr/internal/fsname"
)

// Album is the album-level metadata the layout depends on.
type Album struct {
	AlbumArtist  string
	Artist       string // fallback when there is no album artist
	Title        string
	Grouping     string   // genre folder; "" files under Unknown
	ReleaseTypes []string // MusicBrainz primary + secondary types, e.g. album, soundtrack
	OriginalYear string
	OriginalDate string
	Date         string
	TotalDiscs   int
}

// Track is the per-track metadata the file name depends on.
type Track struct {
	Disc   int
	Number int
	Title  string
}

// VariousArtists is MusicBrainz's (and Picard's va_name) album artist for
// compilations.
const VariousArtists = "Various Artists"

// scriptChars are the characters the naming script's $rreplace turns into "~".
var scriptChars = regexp.MustCompile(`[*|:"<>?]`)

// winChars are the characters Picard's windows compatibility replaces with "_"
// once the script has run (the script already took care of the rest).
var winChars = strings.NewReplacer(`\`, "_")

// value prepares a tag value for use in a path: Picard replaces the directory
// separator inside values (replace_dir_separator = "_") before the script runs.
func value(s string) string { return strings.ReplaceAll(s, "/", "_") }

// Subdir is the top-level archive folder for an album.
func Subdir(a Album) string {
	sub := "Archive Albums"
	if hasType(a.ReleaseTypes, "soundtrack") {
		sub = "Archive Soundtracks"
	}
	if hasType(a.ReleaseTypes, "compilation") && a.AlbumArtist == VariousArtists {
		sub = "Archive Compilations"
	}
	return sub
}

// AlbumDir is the album's folder relative to the library root.
func AlbumDir(a Album) string {
	grouping := a.Grouping
	if grouping == "" {
		grouping = "Unknown"
	}
	artist := a.AlbumArtist
	if artist == "" {
		artist = a.Artist
	}
	year := "0000"
	for _, y := range []string{a.OriginalYear, a.OriginalDate, a.Date} {
		if y != "" {
			year = left(y, 4)
			break
		}
	}
	album := strings.ReplaceAll(value(a.Title), "...", "…")
	name := value(artist) + " - " + year + " - " + album
	return filepath.Join(clean(Subdir(a), value(grouping), name)...)
}

// TrackFile is a track's file name (without extension). Multi-disc albums
// prefix the disc number straight onto the two-digit track number ("103").
func TrackFile(a Album, t Track) string {
	prefix := ""
	if a.TotalDiscs != 1 && t.Disc > 0 {
		prefix = strconv.Itoa(t.Disc)
	}
	name := fmt.Sprintf("%s%02d - %s", prefix, t.Number, value(t.Title))
	name = winChars.Replace(scriptChars.ReplaceAllString(name, "~"))
	// The extension follows, so a trailing dot is kept ("Instr..m4a"); only
	// directory names get the dot rule.
	if strings.HasPrefix(name, ".") {
		name = "_" + name[1:]
	}
	return fsname.Truncate(name, 255)
}

// Path is a track's full path under root, with ext (e.g. ".m4a").
func Path(root string, a Album, t Track, ext string) string {
	return filepath.Join(root, AlbumDir(a), TrackFile(a, t)+ext)
}

// clean applies the script's character replacement and Picard's path rules to
// each directory component.
func clean(parts ...string) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		p = scriptChars.ReplaceAllString(p, "~")
		p = winChars.Replace(p)
		// Picard replaces a dot at the start or end of a component with "_"
		// ("DAMN." -> "DAMN_"), since both are trouble on Windows.
		if strings.HasPrefix(p, ".") {
			p = "_" + p[1:]
		}
		if strings.HasSuffix(p, ".") {
			p = p[:len(p)-1] + "_"
		}
		out[i] = fsname.Truncate(p, 255)
	}
	return out
}

func hasType(types []string, want string) bool {
	for _, t := range types {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

func left(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
