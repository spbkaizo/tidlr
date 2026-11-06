package filing

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckMounted(t *testing.T) {
	dir := t.TempDir()
	if err := CheckMounted(dir, ""); err != nil {
		t.Errorf("writable local dir: %v", err)
	}
	if err := CheckMounted(filepath.Join(dir, "missing"), ""); !errors.Is(err, ErrNotMounted) {
		t.Errorf("missing dir: err = %v, want ErrNotMounted", err)
	}
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o644)
	if err := CheckMounted(file, ""); !errors.Is(err, ErrNotMounted) {
		t.Errorf("file: err = %v, want ErrNotMounted", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("write probe left behind: %v", entries)
	}
}

func TestSameDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not implemented on Windows")
	}
	a, b := t.TempDir(), t.TempDir()
	if same, ok := sameDevice(a, b); !ok || !same {
		t.Errorf("two temp dirs: same=%v ok=%v, want same", same, ok)
	}
}

func TestUnderMount(t *testing.T) {
	for dir, want := range map[string]bool{
		"/Volumes/music/FLAC": true, "/mnt/nas": true, "/media/usb": true,
		"/Volumes": false, "/Users/me/Music": false, "/Volumesque/x": false,
	} {
		if got := underMount(dir); got != want {
			t.Errorf("underMount(%q) = %v, want %v", dir, got, want)
		}
	}
}
