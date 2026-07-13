// Package downloader fetches albums from Tidal. It defines a swappable
// Downloader interface so the concrete backend (currently the `tiddl` CLI) can
// be replaced when the upstream tool inevitably churns.
package downloader

import (
	"context"

	"github.com/simonb/tidlr/internal/adm"
)

// Result is the outcome of a successful download.
type Result struct {
	// AudioDir is the directory containing the downloaded album's audio files.
	AudioDir string
	// Lossless is true when the download is lossless FLAC (the normal case),
	// false when only a lossy edition was available (e.g. Dolby Atmos) and was
	// taken as a fallback. This tells the converter whether to transcode FLAC
	// to ALAC or pass the files through unchanged.
	Lossless bool
}

// ErrNoMatch indicates the release could not be confidently matched to any
// Tidal album (e.g. a garbled ADM title). Callers should skip, not retry.
type ErrNoMatch struct{ Query string }

func (e ErrNoMatch) Error() string { return "no confident Tidal match for " + e.Query }

// Downloader fetches a release's lossless audio to local disk.
type Downloader interface {
	// Download fetches the given release. Implementations should be safe to
	// cancel via ctx.
	Download(ctx context.Context, r adm.Release) (Result, error)
}
