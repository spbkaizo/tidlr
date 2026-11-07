package filing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotMounted means the library is unreachable or is not on its own mounted
// filesystem, so writing there would fill the local disk instead.
var ErrNotMounted = errors.New("library not mounted")

// mountPrefixes are where removable and network filesystems are mounted; a
// library under one of them must be a real mount, not a leftover folder.
var mountPrefixes = []string{"/Volumes/", "/mnt/", "/media/"}

// CheckMounted verifies the library at dir is usable: it exists, is a
// directory, accepts writes, and — when it lives under a mount location —
// sits on a different filesystem from local, the user's home. A share that
// has dropped can leave its mount point behind as an empty local folder;
// this catches that before anything is written there.
func CheckMounted(dir, local string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: %s is not reachable (is the share mounted?)", ErrNotMounted, dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrNotMounted, dir)
	}
	if underMount(dir) && local != "" {
		if same, ok := sameDevice(dir, local); ok && same {
			return fmt.Errorf("%w: %s is on the local disk, not the share (is the share mounted?)", ErrNotMounted, dir)
		}
	}
	probe, err := os.CreateTemp(dir, ".tidlr-write-check-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable: %v", ErrNotMounted, dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

func underMount(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for _, p := range mountPrefixes {
		if strings.HasPrefix(abs+"/", p) && len(abs)+1 > len(p) {
			return true
		}
	}
	return false
}
