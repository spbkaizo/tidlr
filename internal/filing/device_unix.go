//go:build !windows

package filing

import (
	"os"
	"syscall"
)

// sameDevice reports whether a and b are on the same filesystem. ok is false
// when that cannot be determined.
func sameDevice(a, b string) (same, ok bool) {
	da, okA := device(a)
	db, okB := device(b)
	if !okA || !okB {
		return false, false
	}
	return da == db, true
}

func device(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
