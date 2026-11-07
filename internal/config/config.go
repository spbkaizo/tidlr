// Package config loads tidlr runtime configuration from a TOML file, applying
// sensible defaults so a zero-config run still works.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/simonb/tidlr/internal/filing"
)

// Config holds all tunable settings for the pipeline.
type Config struct {
	// OutputDir is where final ALAC (.m4a) files are written, laid out as
	// <OutputDir>/<Artist>/<Album>/<Track>.m4a.
	OutputDir string `toml:"output_dir"`
	// WorkDir holds the SQLite queue DB and the scratch area where FLACs are
	// downloaded before conversion.
	WorkDir string `toml:"work_dir"`

	// FFmpegBin is the path to the ffmpeg executable.
	FFmpegBin string `toml:"ffmpeg_bin"`

	// AuthFile is the Tidal token store. Empty => reuse tiddl's
	// ~/.tiddl/auth.json (so an existing tiddl login keeps working).
	AuthFile string `toml:"auth_file"`

	// Quality is the track quality (low|normal|high|max). max = HiRes lossless.
	Quality string `toml:"quality"`
	// DownloadThreads is the per-album concurrent track-download count.
	DownloadThreads int `toml:"download_threads"`

	// DownloadWorkers is how many albums we download concurrently.
	DownloadWorkers int `toml:"download_workers"`
	// ConvertWorkers is how many albums we convert (FLAC->ALAC) concurrently.
	ConvertWorkers int `toml:"convert_workers"`

	// KeepFLAC, when true, delivers the source FLACs into the output library
	// alongside the ALAC files. Default false. (cover.jpg is always delivered.)
	KeepFLAC bool `toml:"keep_flac"`

	// Progress, when true (the default), renders the live per-track download
	// display during `run`/`sync`, the same one `-album` shows. The display can
	// only draw one album at a time, so enabling it forces albums to download
	// serially — set it false to keep DownloadWorkers albums in flight instead.
	// It is a no-op on a non-TTY, where DownloadWorkers is honoured regardless.
	Progress bool `toml:"progress"`

	// LibraryDir, when set, replaces Picard: each downloaded album is
	// identified on MusicBrainz, tagged the way Picard tags, and filed under
	// LibraryDir as LibraryLayout says. Empty keeps albums in OutputDir with
	// basic tags only.
	LibraryDir string `toml:"library_dir"`
	// LibraryLayout is "artist" (<Artist>/<Year> - <Album>/, the default) or
	// "genre" (Archive Albums/<Genre>/<Artist> - <Year> - <Album>/).
	LibraryLayout filing.Layout `toml:"library_layout"`
	// ReviewDir holds albums that could not be identified or filed, with
	// their basic tags. Default <OutputDir>/_review.
	ReviewDir string `toml:"review_dir"`
	// LastFMAPIKey enables genre (and so genre-folder) lookup on Last.fm, as
	// the lastfmng Picard plugin did. Without it genres come from MusicBrainz.
	LastFMAPIKey string `toml:"lastfm_api_key"`
	// LastFMNGDefaults is the lastfmng Picard plugin's defaults.ini, read for
	// its genre lists. Empty finds it in Picard's plugin folder if installed;
	// otherwise the built-in lists are used (ID3 genre names for the folder,
	// MusicBrainz's genre list for genres).
	LastFMNGDefaults string `toml:"lastfmng_defaults"`
	// AtomicParsleyBin writes the MusicBrainz tags (brew install atomicparsley).
	AtomicParsleyBin string `toml:"atomicparsley_bin"`
	// GenreMerge maps genre folders onto others, collapsing near duplicates
	// ("Punk Rock" = "Punk").
	GenreMerge map[string]string `toml:"genre_merge"`
	// KeepArtistTogether files an album with the artist's other albums when
	// their folder is also one of the album's own genres. Default true.
	KeepArtistTogether bool `toml:"keep_artist_together"`
}

// Default returns a Config populated with the project's chosen defaults.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		OutputDir:          filepath.Join(home, "Music", "tidlr"),
		WorkDir:            filepath.Join(home, ".local", "share", "tidlr"),
		FFmpegBin:          "ffmpeg",
		AuthFile:           "", // empty => tidal.DefaultAuthPath() (~/.tiddl/auth.json)
		Quality:            "max",
		DownloadThreads:    8,
		DownloadWorkers:    3,
		ConvertWorkers:     4,
		KeepFLAC:           false,
		Progress:           true,
		AtomicParsleyBin:   "AtomicParsley",
		KeepArtistTogether: true,
	}
}

// Load reads the TOML file at path over the defaults. A missing file is not an
// error: the defaults are returned. Relative ~ in paths is expanded.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return cfg, fmt.Errorf("parsing config %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("reading config %s: %w", path, err)
		}
	}
	cfg.OutputDir = expandHome(cfg.OutputDir)
	cfg.WorkDir = expandHome(cfg.WorkDir)
	cfg.AuthFile = expandHome(cfg.AuthFile)
	cfg.LibraryDir = expandHome(cfg.LibraryDir)
	cfg.ReviewDir = expandHome(cfg.ReviewDir)
	cfg.LastFMNGDefaults = expandHome(cfg.LastFMNGDefaults)
	if cfg.ReviewDir == "" {
		cfg.ReviewDir = filepath.Join(cfg.OutputDir, "_review")
	}
	layout, err := filing.ParseLayout(string(cfg.LibraryLayout))
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	cfg.LibraryLayout = layout
	return cfg, nil
}

// DownloadDir is the scratch directory under WorkDir where FLACs are fetched.
func (c Config) DownloadDir() string { return filepath.Join(c.WorkDir, "downloads") }

// DBPath is the SQLite queue database location.
func (c Config) DBPath() string { return filepath.Join(c.WorkDir, "queue.db") }

// AuthPath returns the configured Tidal token store path ("" lets the tidal
// package fall back to its default ~/.tiddl/auth.json).
func (c Config) AuthPath() string { return c.AuthFile }

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
