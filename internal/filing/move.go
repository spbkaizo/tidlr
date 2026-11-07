package filing

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ErrExists means the destination album folder is already there; nothing is
// overwritten (Picard's move_overwrite_existing_files = false).
var ErrExists = errors.New("album folder already exists")

// Move is one file to place: From (absolute) to Name inside the album folder.
type Move struct {
	From string
	Name string
}

// Place moves files into a new album folder dest. It builds the folder under
// a temporary name beside dest and renames it into place at the end, so an
// interrupted move (or a full NAS) never leaves a half-filled album where the
// library expects a whole one. Moving across filesystems falls back to
// copy-then-delete.
func Place(dest string, files []Move) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	partial := dest + ".tidlr-partial"
	os.RemoveAll(partial) // leftover from an interrupted run
	if err := os.Mkdir(partial, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if err := moveFile(f.From, filepath.Join(partial, f.Name)); err != nil {
			os.RemoveAll(partial)
			return fmt.Errorf("moving %s: %w", filepath.Base(f.From), err)
		}
	}
	if err := os.Rename(partial, dest); err != nil {
		os.RemoveAll(partial)
		return err
	}
	return nil
}

// moveFile renames src to dst, copying when they are on different
// filesystems (local scratch to a network share).
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
		return err
	}
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
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
