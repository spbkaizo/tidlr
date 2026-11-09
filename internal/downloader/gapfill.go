package downloader

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/simonb/tidlr/internal/convert"
	"github.com/simonb/tidlr/internal/tidal"
)

// GapFiller fetches a track an album lists but Tidal does not serve, from
// wherever else on Tidal it is (a single, a compilation, the original of a
// borrowed recording), and converts it into the album's folder under the
// album's numbering and cover (organize.Filler).
type GapFiller struct {
	Matcher    *tidal.Matcher
	Downloader *tidal.Downloader
	Converter  *convert.Converter
	Quality    string
	BaseDir    string // scratch root
}

// Fill implements organize.Filler.
func (g *GapFiller) Fill(ctx context.Context, dir string, album tidal.Album, volume, number int, want tidal.WantTrack) (tidal.Track, error) {
	found, err := g.Matcher.FindTrack(ctx, want)
	if err != nil {
		return tidal.Track{}, err
	}
	if err := os.MkdirAll(g.BaseDir, 0o755); err != nil {
		return tidal.Track{}, err
	}
	tmp, err := os.MkdirTemp(g.BaseDir, "fill-")
	if err != nil {
		return tidal.Track{}, err
	}
	defer os.RemoveAll(tmp)
	// The album's cover, so the converter embeds it as in the other tracks.
	if err := copyFile(filepath.Join(dir, "cover.jpg"), filepath.Join(tmp, "cover.jpg")); err != nil && !os.IsNotExist(err) {
		return tidal.Track{}, err
	}
	// Named and tagged as the album's track, under MusicBrainz's title.
	slot := found
	slot.Title, slot.VolumeNumber, slot.TrackNumber = want.Title, volume, number
	if _, err := g.Downloader.DownloadAs(ctx, slot, album, volume, number, g.Quality, tmp); err != nil {
		return tidal.Track{}, err
	}
	if err := g.Converter.ConvertInto(ctx, tmp, dir); err != nil {
		return tidal.Track{}, err
	}
	return slot, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
