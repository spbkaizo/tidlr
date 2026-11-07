package organize

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// libraryIndex finds albums already in the library by a loose artist + title
// key, so a copy filed under a slightly different name ("Nick Cave and the
// Bad Seeds" vs "Nick Cave & The Bad Seeds", a "(Deluxe)" edition) still
// counts as had.
type libraryIndex struct {
	mu   sync.Mutex
	dirs map[string][]libraryEntry // albumKey -> albums
}

type libraryEntry struct{ year, dir string }

// editionRe matches a bracketed edition marker: the same album, packaged
// differently. "(Remixes)", "(Instrumentals)" and "(Taylor's Version)" are
// other albums and stay part of the title.
var editionRe = regexp.MustCompile(`(?i)[(\[][^)\]]*\b(deluxe|edition|remaster(ed)?|expanded|explicit|bonus|anniversary)\b[^)\]]*[)\]]`)

var nonAlnumRe = regexp.MustCompile(`[^\pL\pN]+`)

// albumKey loosens an artist and title for comparison: edition markers,
// case and punctuation are dropped, "&" is "and", and a leading "The" is
// ignored.
func albumKey(artist, title string) string {
	norm := func(s string) string {
		s = editionRe.ReplaceAllString(s, " ")
		s = strings.ReplaceAll(strings.ToLower(s), "&", " and ")
		s = strings.TrimSpace(nonAlnumRe.ReplaceAllString(s, " "))
		return strings.TrimPrefix(s, "the ")
	}
	a, t := norm(artist), norm(title)
	if a == "" || t == "" {
		return ""
	}
	return a + "\x00" + t
}

// yearTitleRe splits an artist-layout album folder, "<Year> - <Album>".
var yearTitleRe = regexp.MustCompile(`^(\d{4}) - (.+)$`)

// loadLibraryIndex reads album folder names under library in either layout:
// <Archive>/<Genre>/<Artist> - <Year> - <Album> or <Artist>/<Year> - <Album>.
func loadLibraryIndex(library string) *libraryIndex {
	idx := &libraryIndex{dirs: map[string][]libraryEntry{}}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if m := albumDirRe.FindStringSubmatch(e.Name()); m != nil {
				idx.add(m[1], strings.TrimPrefix(e.Name(), m[0]), m[2], path)
				continue
			}
			if m := yearTitleRe.FindStringSubmatch(e.Name()); m != nil && depth == 1 {
				idx.add(filepath.Base(dir), m[2], m[1], path)
				continue
			}
			if depth < 2 {
				walk(path, depth+1)
			}
		}
	}
	walk(library, 0)
	return idx
}

func (idx *libraryIndex) add(artist, title, year, dir string) {
	k := albumKey(artist, title)
	if k == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.dirs[k] = append(idx.dirs[k], libraryEntry{year, dir})
}

// find returns an album matching artist and title; when year is given, and
// the album's folder has one, they must agree ("0000" is unknown).
func (idx *libraryIndex) find(artist, title, year string) (string, bool) {
	k := albumKey(artist, title)
	if k == "" {
		return "", false
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, e := range idx.dirs[k] {
		if year == "" || year == "0000" || e.year == "0000" || e.year == year {
			return e.dir, true
		}
	}
	return "", false
}

// InLibrary reports whether an album by artist with this title (and year,
// if known: "" otherwise) is already in the library, loosely matched, and
// where. The library is indexed on first use; albums filed since are added
// as they are filed.
func (o *Organizer) InLibrary(artist, title, year string) (string, bool) {
	o.libraryOnce.Do(func() {
		if o.LibraryDir != "" {
			o.library = loadLibraryIndex(o.LibraryDir)
		}
	})
	if o.library == nil {
		return "", false
	}
	return o.library.find(artist, title, year)
}
