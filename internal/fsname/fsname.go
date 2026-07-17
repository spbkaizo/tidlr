// Package fsname builds strings that are safe to use as filesystem path
// components, on the byte-length limits real filesystems impose as well as
// the character rules.
package fsname

import (
	"strings"
	"unicode/utf8"
)

// MaxComponent is the per-component byte budget. APFS, ext4 and NTFS all cap a
// single name at 255 bytes; Sanitize leaves headroom below that so callers can
// append suffixes (".raw.flac", os.CreateTemp's ".dl-<random>") to a sanitized
// base without overrunning the real limit.
const MaxComponent = 200

var replacer = strings.NewReplacer(
	"/", "-", "\\", "-", ":", "-", "*", "", "?", "",
	"\"", "", "<", "", ">", "", "|", "-",
)

// Sanitize makes s safe as a single path component: it strips path separators
// and characters that are illegal or awkward on common filesystems, then caps
// the result at MaxComponent bytes.
//
// Length is measured in bytes, not runes, because that is what filesystems
// limit. Titles of heavily decorated tracks (combining marks, CJK, emoji) run
// several bytes per rune and can exceed the cap while looking short.
func Sanitize(s string) string {
	s = replacer.Replace(strings.TrimSpace(s))
	s = truncateBytes(s, MaxComponent)
	// Trailing dots and spaces are stripped by Windows and confuse some tools.
	s = strings.TrimRight(s, " .")
	if s == "" {
		return "Unknown"
	}
	return s
}

// truncateBytes shortens s to at most n bytes without splitting a rune.
// Combining marks left stranded at the cut are dropped along with the base
// rune they attach to, so truncation cannot graft marks onto a new base.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	// Back off to a rune boundary.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
