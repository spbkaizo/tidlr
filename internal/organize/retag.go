package organize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simonb/tidlr/internal/genre"
	"github.com/simonb/tidlr/internal/musicbrainz"
	"github.com/simonb/tidlr/internal/tagger"
)

// Retag is a planned genre fix for an album already in the library: its new
// genre tags and the genre folder it belongs in. Only the genre part of the
// path changes; the album folder keeps its name.
type Retag struct {
	Dir      string // current album folder
	NewDir   string // where it belongs; == Dir when the folder is right
	Current  string // current genre folder name
	Result   genre.Result
	Source   string // "last.fm", "musicbrainz" or "" (no genre found)
	Files    []string
	SkipNote string // set when the album cannot be retagged, with the reason
}

// Moves reports whether applying the plan moves the album.
func (r Retag) Moves() bool { return r.NewDir != r.Dir }

// AlbumDirs lists the album folders under root: folders holding .m4a files.
func AlbumDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".m4a") {
			dirs = append(dirs, filepath.Dir(path))
			return filepath.SkipDir // the rest of this folder is the same album
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

// PlanRetag works out an album's genre from its own tags (album artist,
// album, and each track's artist and title, as lastfmng read them), falling
// back to the MusicBrainz genres of the release its tags name.
func (o *Organizer) PlanRetag(ctx context.Context, dir string) (Retag, error) {
	plan := Retag{Dir: dir, NewDir: dir, Current: filepath.Base(filepath.Dir(dir))}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.m4a"))
	sort.Strings(matches)
	if len(matches) == 0 {
		plan.SkipNote = "no .m4a files"
		return plan, nil
	}
	plan.Files = matches

	ga := genre.Album{}
	var albumID string
	for i, f := range matches {
		tags, err := o.Tagger.Read(ctx, f)
		if err != nil {
			return plan, err
		}
		if i == 0 {
			ga.AlbumArtist = firstNonEmpty(tags["aART"], tags["©ART"])
			ga.Title = tags["©alb"]
			albumID = tags["MusicBrainz Album Id"]
		}
		ga.Tracks = append(ga.Tracks, genre.Track{Artist: tags["©ART"], Title: tags["©nam"]})
	}
	if ga.AlbumArtist == "" || ga.Title == "" {
		plan.SkipNote = "no album artist/album tags"
		return plan, nil
	}

	plan.Result, plan.Source = o.genreFor(ctx, ga, func() []musicbrainz.Genre {
		if albumID == "" {
			return nil
		}
		r, err := o.MB.LookupRelease(ctx, albumID)
		if err != nil {
			o.logf("warning: MusicBrainz release %s: %v", albumID, err)
			return nil
		}
		return append(r.ReleaseGroup.Genres, r.Genres...)
	})
	grouping := orUnknown(plan.Result.Grouping)
	if grouping != plan.Current {
		plan.NewDir = filepath.Join(filepath.Dir(filepath.Dir(dir)), clean(grouping), filepath.Base(dir))
	}
	return plan, nil
}

// clean makes a genre safe as a folder name the way the naming script would.
func clean(s string) string {
	return strings.NewReplacer("/", "_", ":", "~", "?", "~", "*", "~", "|", "~", `"`, "~", "<", "~", ">", "~").Replace(s)
}

// ApplyRetag writes the plan's genre tags to every track, then moves the
// album into its genre folder. It never overwrites an existing folder.
func (o *Organizer) ApplyRetag(ctx context.Context, plan Retag) error {
	if plan.SkipNote != "" || plan.Source == "" {
		return nil // nothing better than what is there
	}
	if err := o.checkMounted(); err != nil {
		return err
	}
	if plan.Moves() {
		if _, err := os.Stat(plan.NewDir); err == nil {
			return fmt.Errorf("%s already exists", plan.NewDir)
		}
	}
	g := plan.Result
	genres := strings.Join(g.Genres, tagger.MultiSep)
	moods := strings.Join(g.Moods, tagger.MultiSep)
	t := tagger.Tags{
		Genre:    firstNonEmpty(genres, g.Grouping),
		Grouping: g.Grouping,
		Freeform: []tagger.Field{
			{Name: "albumgrouping", Value: g.Grouping},
			{Name: "albumgenre", Value: genres},
			{Name: "albummood", Value: moods},
			{Name: "MOOD", Value: moods},
		},
	}
	for _, f := range plan.Files {
		if err := o.Tagger.Write(ctx, f, t); err != nil {
			return err
		}
	}
	if !plan.Moves() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(plan.NewDir), 0o755); err != nil {
		return err
	}
	if err := os.Rename(plan.Dir, plan.NewDir); err != nil {
		return err
	}
	os.Remove(filepath.Dir(plan.Dir)) // the old genre folder, if now empty
	return nil
}
