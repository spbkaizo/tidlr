package tidal

import (
	"regexp"
	"slices"
	"strings"
)

// numberWords maps spelled-out and roman numbers to digits. "i", "v" and "x"
// are left out: as words they are far more often a pronoun, an initial or a
// multiplication sign ("I Shouldn't Be Telling You This", "m b v",
// "4 X 4 = 12") than a number.
var numberWords = map[string]string{
	"one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
	"six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10",
	"ii": "2", "iii": "3", "iv": "4", "vi": "6", "vii": "7", "viii": "8", "ix": "9",
}

var digitsRe = regexp.MustCompile(`[0-9]+`)

// TitleNumbers returns the set of numbers in a normalized title, as digits.
// Sequels differ only by their number ("Pink Friday" vs "Pink Friday 2",
// "Tomorrows II" vs "Tomorrows III") yet score highly as strings, so titles
// whose numbers differ are never the same album.
func TitleNumbers(title string) string {
	set := map[string]bool{}
	for _, w := range strings.Fields(title) {
		if d, ok := numberWords[w]; ok {
			set[d] = true
		}
	}
	for _, d := range digitsRe.FindAllString(title, -1) {
		set[strings.TrimLeft(d, "0")] = true
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}
