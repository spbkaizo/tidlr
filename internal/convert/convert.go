// Package convert transcodes downloaded FLAC albums to ALAC (.m4a), preserving
// tags and embedded cover art, and delivers them into the output library tree.
package convert

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/simonb/tidlr/internal/adm"
	"github.com/simonb/tidlr/internal/fsname"
)

// Converter turns a downloaded FLAC album into ALAC in the output library.
type Converter struct {
	FFmpegBin string // path to ffmpeg
	OutputDir string // library root: <OutputDir>/<Artist>/<Album>/*.m4a
	KeepFLAC  bool   // if true, deliver source FLACs alongside the ALAC and keep the scratch copies
}

// Convert delivers an album from srcDir into the output tree and returns the
// destination album directory.
//
// When lossless is true (the normal case), every .flac is transcoded to ALAC.
// When false, the source was a lossy fallback (e.g. a Dolby Atmos edition that
// only exists as .m4a); those files are copied through unchanged since there is
// no lossless master to transcode.
// Convert delivers an album's downloaded files to <OutputDir>/<Artist>/<Album>/.
// The lossless argument is accepted for API stability but no longer consulted:
// delivery is decided per file by extension (.flac -> ALAC, lossy .m4a/.mp4
// passed through).
func (c *Converter) Convert(ctx context.Context, r adm.Release, srcDir string, lossless bool) (string, error) {
	albumOut := filepath.Join(c.OutputDir, sanitize(r.Artist), sanitize(r.Album))
	return c.convertDir(ctx, srcDir, albumOut)
}

// ConvertFlat delivers a source directory to <OutputDir>/<name>/ (a flat layout
// used for playlists, which are not organized by artist/album). Returns the
// destination directory.
func (c *Converter) ConvertFlat(ctx context.Context, name, srcDir string, lossless bool) (string, error) {
	out := filepath.Join(c.OutputDir, sanitize(name))
	return c.convertDir(ctx, srcDir, out)
}

// convertDir transcodes/copies every audio file in srcDir into outDir. A sibling
// cover.jpg is embedded into transcoded tracks; tracks that already embed art
// (e.g. playlist tracks) keep it via the transcode's stream copy.
func (c *Converter) convertDir(ctx context.Context, srcDir, outDir string) (string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", fmt.Errorf("reading source dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}

	// A sibling cover.jpg (album downloads) is embedded into each track during
	// transcode. Empty if absent (playlist tracks embed their own art already).
	cover := ""
	if p := filepath.Join(srcDir, "cover.jpg"); fileExists(p) {
		cover = p
	}

	delivered := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		src := filepath.Join(srcDir, e.Name())
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))

		// Decide per file by extension so a mixed playlist (mostly lossless FLAC
		// with the odd Dolby Atmos track) is handled correctly: .flac is
		// transcoded to ALAC; an already-lossy .m4a/.mp4 (eac3 Atmos) passes
		// through as-is.
		switch ext {
		case ".flac":
			dst := filepath.Join(outDir, base+".m4a")
			if err := c.transcode(ctx, src, cover, dst); err != nil {
				return "", fmt.Errorf("converting %s: %w", e.Name(), err)
			}
			// KeepFLAC also delivers the lossless source alongside the ALAC, for
			// players that cannot read ALAC (car head units, some streamers).
			if c.KeepFLAC {
				if err := copyFile(src, filepath.Join(outDir, e.Name())); err != nil {
					return "", fmt.Errorf("copying source FLAC %s: %w", e.Name(), err)
				}
			}
		case ".m4a", ".mp4":
			dst := filepath.Join(outDir, e.Name())
			if err := copyFile(src, dst); err != nil {
				return "", fmt.Errorf("copying %s: %w", e.Name(), err)
			}
		default:
			continue // skip covers, lyrics, and mismatched extensions
		}
		delivered++

		if !c.KeepFLAC {
			if err := os.Remove(src); err != nil {
				return "", fmt.Errorf("removing source %s: %w", src, err)
			}
		}
	}

	if delivered == 0 {
		return "", fmt.Errorf("no audio files delivered from %s", srcDir)
	}

	// Deliver the cover as a separate file too, for devices that need artwork
	// next to the audio rather than embedded. Handled outside the loop so it is
	// never counted as a delivered track nor removed as a consumed source.
	if c.KeepFLAC && cover != "" {
		if err := copyFile(cover, filepath.Join(outDir, "cover.jpg")); err != nil {
			return "", fmt.Errorf("copying cover: %w", err)
		}
	}
	return outDir, nil
}

// copyFile copies src to dst, writing via a temp file for atomicity.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// transcode runs the ffmpeg FLAC->ALAC command, encoding lossless ALAC and
// embedding cover art. Art comes either from a picture already embedded in the
// source FLAC (carried by `-map 0 -c:v copy`) or, when coverPath is non-empty,
// from that external jpeg added as an attached picture. `+faststart` moves the
// moov atom to the front.
func (c *Converter) transcode(ctx context.Context, src, coverPath, dst string) error {
	tmp := dst + ".tmp.m4a" // write to temp so a crash never leaves a partial .m4a

	args := []string{"-y", "-nostdin", "-i", src}
	if coverPath != "" {
		// Second input is the cover; map it as an attached picture on the output.
		args = append(args,
			"-i", coverPath,
			"-map", "0:a", "-map", "1:v",
			"-c:a", "alac",
			"-c:v", "copy",
			"-disposition:v", "attached_pic",
		)
	} else {
		args = append(args,
			"-map", "0",
			"-c:a", "alac",
			"-c:v", "copy",
		)
	}
	args = append(args, "-movflags", "+faststart", tmp)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.FFmpegBin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%w: %s", err, tail(stderr.String()))
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// sanitize strips path separators and characters that are awkward in filenames
// so artist/album names form safe directory names.
func sanitize(s string) string { return fsname.Sanitize(s) }

// tail returns the last few lines of ffmpeg stderr for concise error messages.
func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}
