package tagger

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// atomLine matches AtomicParsley -t output: standard atoms
// (`Atom "©nam" contains: Title`) and freeform ones
// (`Atom "----" [com.apple.iTunes;MusicBrainz Album Id] contains: …`).
var atomLine = regexp.MustCompile(`^Atom "([^"]+)"(?: \[[^;\]]*;([^\]]+)\])? contains: (.*)$`)

// Read returns a file's tags keyed by atom name ("©nam", "aART") or, for
// freeform atoms, by their name ("MusicBrainz Album Id"). Repeated freeform
// atoms (Picard's multi-values) are joined with MultiSep.
func (w Writer) Read(ctx context.Context, path string) (map[string]string, error) {
	bin := w.Bin
	if bin == "" {
		bin = "AtomicParsley"
	}
	out, err := exec.CommandContext(ctx, bin, path, "-t").Output()
	if err != nil {
		return nil, fmt.Errorf("AtomicParsley -t %s: %w", path, err)
	}
	return parseAtoms(string(out)), nil
}

func parseAtoms(out string) map[string]string {
	tags := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(out, "\ufeff")))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // lyrics can be long
	for sc.Scan() {
		m := atomLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		key := m[1]
		if m[2] != "" {
			key = m[2]
		}
		if prev, ok := tags[key]; ok && m[2] != "" {
			tags[key] = prev + MultiSep + m[3]
		} else {
			tags[key] = m[3]
		}
	}
	return tags
}
