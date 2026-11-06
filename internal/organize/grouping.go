package organize

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/simonb/tidlr/internal/filing"
	"github.com/simonb/tidlr/internal/genre"
)

// albumDirRe splits an album folder name into its artist part.
var albumDirRe = regexp.MustCompile(`^(.+?) - (\d{4}) - `)

// folderArtist is the artist part of an album folder name, normalized for
// comparison ("" when the name does not follow the naming scheme).
func folderArtist(name string) string {
	m := albumDirRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m[1]))
}

// artistIndex records which genre folders hold each artist's albums, per
// archive ("Archive Albums", ...), from the library's folder names.
type artistIndex struct {
	mu     sync.Mutex
	albums map[string]map[string]string // archive+"\x00"+artist -> album dir -> genre folder
}

// loadArtistIndex scans <library>/<archive>/<genre>/<album> folder names.
func loadArtistIndex(library string) *artistIndex {
	idx := &artistIndex{albums: map[string]map[string]string{}}
	archives, _ := os.ReadDir(library)
	for _, a := range archives {
		if !a.IsDir() || !strings.HasPrefix(a.Name(), "Archive ") {
			continue
		}
		genres, _ := os.ReadDir(filepath.Join(library, a.Name()))
		for _, g := range genres {
			if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
				continue
			}
			albums, _ := os.ReadDir(filepath.Join(library, a.Name(), g.Name()))
			for _, al := range albums {
				if al.IsDir() {
					idx.add(filepath.Join(library, a.Name(), g.Name(), al.Name()))
				}
			}
		}
	}
	return idx
}

// indexKey returns the index key and genre folder for an album dir
// <library>/<archive>/<genre>/<album>.
func indexKey(albumDir string) (key, genre string) {
	genreDir := filepath.Dir(albumDir)
	archive := filepath.Base(filepath.Dir(genreDir))
	return archive + "\x00" + folderArtist(filepath.Base(albumDir)), filepath.Base(genreDir)
}

func (idx *artistIndex) add(albumDir string) {
	key, genre := indexKey(albumDir)
	if strings.HasSuffix(key, "\x00") {
		return // folder name without an artist part
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.albums[key] == nil {
		idx.albums[key] = map[string]string{}
	}
	idx.albums[key][albumDir] = genre
}

func (idx *artistIndex) remove(albumDir string) {
	key, _ := indexKey(albumDir)
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.albums[key], albumDir)
}

// moved updates the index after an album folder moved.
func (idx *artistIndex) moved(from, to string) {
	idx.remove(from)
	idx.add(to)
}

// artistFolders counts the genre folders holding an artist's albums in an
// archive, leaving out the album at exclude, Unknown, and folders merged away.
func (idx *artistIndex) artistFolders(archive, albumName, exclude string, merge func(string) string) map[string]int {
	artist := folderArtist(albumName)
	if artist == "" || artist == strings.ToLower(filing.VariousArtists) {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	counts := map[string]int{}
	for dir, genre := range idx.albums[archive+"\x00"+artist] {
		if dir == exclude || genre == "Unknown" {
			continue
		}
		counts[merge(genre)]++
	}
	return counts
}

// merged applies the genre_merge mapping to a genre folder name.
func (o *Organizer) merged(genre string) string {
	if to, ok := o.Merge[genre]; ok {
		return to
	}
	return genre
}

// decideGrouping turns an album's genre result into the folder it goes in.
// Merged genres are mapped onto their target. Then, with KeepArtistTogether,
// an artist with other albums in the archive keeps them together — gently:
// only folders that are also among the album's own genres (its grouping or
// genre tags) are eligible, so a jungle album by an artist mostly filed under
// Electronic stays in Jungle unless the album itself is tagged electronic.
// An album with no genre at all may join any of the artist's folders. Among
// eligible folders the one holding most of the artist's albums wins; ties go
// to the computed genre, then current (the album's present folder, "" for a
// new album). reason is "artist" when that rule changed the folder.
func (o *Organizer) decideGrouping(res genre.Result, archive, albumName, albumDir, current string) (grouping, reason string) {
	grouping = o.merged(res.Grouping)
	if !o.KeepArtistTogether || o.artists == nil {
		return grouping, ""
	}
	counts := o.artists.artistFolders(archive, albumName, albumDir, o.merged)
	if len(counts) == 0 {
		return grouping, ""
	}
	if res.Grouping != "" || len(res.Genres) > 0 {
		own := map[string]bool{genreKey(grouping): true}
		for _, g := range res.Genres {
			own[genreKey(o.merged(g))] = true
		}
		for g := range counts {
			if !own[genreKey(g)] {
				delete(counts, g)
			}
		}
		if len(counts) == 0 {
			return grouping, ""
		}
	}
	best := 0
	for _, n := range counts {
		best = max(best, n)
	}
	var top []string
	for g, n := range counts {
		if n == best {
			top = append(top, g)
		}
	}
	sort.Strings(top)
	for _, prefer := range []string{grouping, o.merged(current)} {
		for _, g := range top {
			if g == prefer && prefer != "" {
				return g, reasonIf(g != grouping)
			}
		}
	}
	return top[0], reasonIf(top[0] != grouping)
}

// genreKey compares genre names loosely: case, "&" vs "and", punctuation
// ("Drum & Bass" = "drum and bass", "Hip-Hop" = "hip hop").
func genreKey(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", " and "))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func reasonIf(changed bool) string {
	if changed {
		return "artist"
	}
	return ""
}

// loadArtists builds the artist index on first use.
func (o *Organizer) loadArtists() {
	o.artistsOnce.Do(func() {
		if o.KeepArtistTogether && o.LibraryDir != "" {
			o.artists = loadArtistIndex(o.LibraryDir)
		}
	})
}
