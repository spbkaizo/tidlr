package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveEmptyParents(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "8559", "Mount Eerie", "Night Palace")
	other := filepath.Join(root, "9000", "Geese", "Getting Killed")
	for _, d := range []string{album, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.RemoveAll(album)
	removeEmptyParents(album, 2)
	if _, err := os.Stat(filepath.Join(root, "8559")); !os.IsNotExist(err) {
		t.Errorf("empty scratch folders left: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another album's folder went: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("scratch root went: %v", err)
	}
}
