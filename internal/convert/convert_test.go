package convert

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simonb/tidlr/internal/adm"
)

// makeFLAC synthesizes a short tagged FLAC via ffmpeg for hermetic testing.
func makeFLAC(t *testing.T, path, title string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-y", "-nostdin",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-metadata", "title="+title,
		"-metadata", "artist=Test Artist",
		"-c:a", "flac", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg make flac: %v\n%s", err, out)
	}
}

// makeCover writes a tiny solid-color JPEG via ffmpeg for cover-embedding tests.
func makeCover(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-y", "-nostdin",
		"-f", "lavfi", "-i", "color=c=blue:s=64x64:d=1",
		"-frames:v", "1", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg make cover: %v\n%s", err, out)
	}
}

func TestConvertEmbedsSiblingCover(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	srcDir := t.TempDir()
	makeFLAC(t, filepath.Join(srcDir, "01 Song.flac"), "Song") // FLAC has no embedded art
	makeCover(t, filepath.Join(srcDir, "cover.jpg"))

	outDir := t.TempDir()
	c := &Converter{FFmpegBin: "ffmpeg", OutputDir: outDir, KeepFLAC: false}
	albumOut, err := c.Convert(context.Background(), adm.Release{Artist: "A", Album: "B"}, srcDir, true)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	m4as, _ := filepath.Glob(filepath.Join(albumOut, "*.m4a"))
	if len(m4as) != 1 {
		t.Fatalf("expected 1 m4a, got %d", len(m4as))
	}
	// The output should now contain an attached-picture video stream from cover.jpg.
	out, _ := exec.Command("ffmpeg", "-i", m4as[0]).CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "alac") {
		t.Errorf("output not ALAC:\n%s", s)
	}
	if !strings.Contains(s, "Video:") && !strings.Contains(s, "attached pic") {
		t.Errorf("cover not embedded (no video/attached pic stream):\n%s", s)
	}
}

func TestConvertToALAC(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	flacDir := t.TempDir()
	makeFLAC(t, filepath.Join(flacDir, "01 Track One.flac"), "Track One")
	makeFLAC(t, filepath.Join(flacDir, "02 Track Two.flac"), "Track Two")

	outDir := t.TempDir()
	c := &Converter{FFmpegBin: "ffmpeg", OutputDir: outDir, KeepFLAC: false}
	r := adm.Release{Artist: "Test Artist", Album: "Test/Album"} // slash must be sanitized

	albumOut, err := c.Convert(context.Background(), r, flacDir, true)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	// Output goes to sanitized <out>/Test Artist/Test-Album.
	if want := filepath.Join(outDir, "Test Artist", "Test-Album"); albumOut != want {
		t.Errorf("albumOut = %q, want %q", albumOut, want)
	}
	m4as, _ := filepath.Glob(filepath.Join(albumOut, "*.m4a"))
	if len(m4as) != 2 {
		t.Fatalf("expected 2 .m4a, got %d", len(m4as))
	}

	// Source FLACs must be gone (KeepFLAC=false).
	flacs, _ := filepath.Glob(filepath.Join(flacDir, "*.flac"))
	if len(flacs) != 0 {
		t.Errorf("expected FLACs deleted, %d remain", len(flacs))
	}

	// Verify the output is genuinely ALAC.
	out, _ := exec.Command("ffmpeg", "-i", m4as[0]).CombinedOutput()
	if !strings.Contains(string(out), "alac") {
		t.Errorf("output not ALAC:\n%s", out)
	}
	// No stray temp files left behind.
	tmps, _ := filepath.Glob(filepath.Join(albumOut, "*.tmp.m4a"))
	if len(tmps) != 0 {
		t.Errorf("temp files left behind: %v", tmps)
	}
}

func TestConvertLossyPassthrough(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// Simulate an Atmos fallback: an .m4a already-encoded source that must be
	// delivered as-is (not transcoded), with lossless=false.
	srcDir := t.TempDir()
	m4a := filepath.Join(srcDir, "track.m4a")
	cmd := exec.Command("ffmpeg", "-y", "-nostdin",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:a", "aac", m4a)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make m4a: %v\n%s", err, out)
	}

	outDir := t.TempDir()
	c := &Converter{FFmpegBin: "ffmpeg", OutputDir: outDir, KeepFLAC: false}
	albumOut, err := c.Convert(context.Background(), adm.Release{Artist: "A", Album: "B"}, srcDir, false)
	if err != nil {
		t.Fatalf("Convert lossy: %v", err)
	}
	got, _ := filepath.Glob(filepath.Join(albumOut, "*.m4a"))
	if len(got) != 1 {
		t.Fatalf("expected 1 delivered m4a, got %d", len(got))
	}
	// Source consumed (KeepFLAC=false), no leftover temp.
	if _, err := os.Stat(m4a); !os.IsNotExist(err) {
		t.Errorf("source m4a should have been removed")
	}
	tmps, _ := filepath.Glob(filepath.Join(albumOut, "*.tmp"))
	if len(tmps) != 0 {
		t.Errorf("temp files left: %v", tmps)
	}
}

func TestConvertKeepFLAC(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	flacDir := t.TempDir()
	makeFLAC(t, filepath.Join(flacDir, "song.flac"), "Song")
	makeCover(t, filepath.Join(flacDir, "cover.jpg"))

	c := &Converter{FFmpegBin: "ffmpeg", OutputDir: t.TempDir(), KeepFLAC: true}
	albumOut, err := c.Convert(context.Background(), adm.Release{Artist: "A", Album: "B"}, flacDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(flacDir, "song.flac")); err != nil {
		t.Errorf("KeepFLAC=true but source removed: %v", err)
	}
	// The scratch dir is wiped after conversion, so the library copy is what
	// actually survives: KeepFLAC must deliver both formats plus the cover.
	for _, name := range []string{"song.m4a", "song.flac", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(albumOut, name)); err != nil {
			t.Errorf("KeepFLAC=true but %s not delivered to output: %v", name, err)
		}
	}
}

func TestConvertNoKeepFLACOmitsSources(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	flacDir := t.TempDir()
	makeFLAC(t, filepath.Join(flacDir, "song.flac"), "Song")
	makeCover(t, filepath.Join(flacDir, "cover.jpg"))

	c := &Converter{FFmpegBin: "ffmpeg", OutputDir: t.TempDir(), KeepFLAC: false}
	albumOut, err := c.Convert(context.Background(), adm.Release{Artist: "A", Album: "B"}, flacDir, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"song.flac", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(albumOut, name)); err == nil {
			t.Errorf("KeepFLAC=false but %s was delivered to output", name)
		}
	}
	if _, err := os.Stat(filepath.Join(albumOut, "song.m4a")); err != nil {
		t.Errorf("m4a not delivered: %v", err)
	}
}
