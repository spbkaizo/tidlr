package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MP3 encodes downloaded FLAC albums as constant-bitrate MP3, for players
// that cannot read ALAC (car head units, cheap sticks).
type MP3 struct {
	FFmpegBin string // path to ffmpeg (needs libmp3lame)
	Bitrate   string // e.g. "192k"
}

// ConvertDir encodes every .flac in srcDir to <outDir>/<name>.mp3, embedding
// a sibling cover.jpg when present, and copies the cover alongside. Sources
// are left in place. It returns the bytes written.
func (m *MP3) ConvertDir(ctx context.Context, srcDir, outDir string) (int64, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, fmt.Errorf("reading source dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	cover := ""
	if p := filepath.Join(srcDir, "cover.jpg"); fileExists(p) {
		cover = p
	}

	var total int64
	n := 0
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".flac" {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		dst := filepath.Join(outDir, base+".mp3")
		if err := m.encode(ctx, filepath.Join(srcDir, e.Name()), cover, dst); err != nil {
			return 0, fmt.Errorf("encoding %s: %w", e.Name(), err)
		}
		info, err := os.Stat(dst)
		if err != nil {
			return 0, err
		}
		total += info.Size()
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("no FLAC files in %s", srcDir)
	}
	if cover != "" {
		dst := filepath.Join(outDir, "cover.jpg")
		if err := copyFile(cover, dst); err != nil {
			return 0, err
		}
		if info, err := os.Stat(dst); err == nil {
			total += info.Size()
		}
	}
	return total, nil
}

func (m *MP3) encode(ctx context.Context, src, coverPath, dst string) error {
	tmp := dst + ".tmp.mp3"
	args := []string{"-y", "-nostdin", "-i", src}
	if coverPath != "" {
		args = append(args, "-i", coverPath, "-map", "0:a", "-map", "1:v",
			"-c:v", "copy", "-disposition:v", "attached_pic")
	} else {
		args = append(args, "-map", "0:a", "-map", "0:v?", "-c:v", "copy")
	}
	args = append(args, "-c:a", "libmp3lame", "-b:a", m.Bitrate,
		"-id3v2_version", "3", "-write_id3v1", "1", tmp)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, m.FFmpegBin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%w: %s", err, tail(stderr.String()))
	}
	return os.Rename(tmp, dst)
}
