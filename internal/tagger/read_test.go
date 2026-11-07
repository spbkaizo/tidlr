package tagger

import "testing"

func TestParseAtoms(t *testing.T) {
	out := "\ufeffAtom \"©nam\" contains: Hold It\n" +
		"Atom \"aART\" contains: Various Artists\n" +
		"Atom \"----\" [com.apple.iTunes;MusicBrainz Album Type] contains: album\n" +
		"Atom \"----\" [com.apple.iTunes;MusicBrainz Album Type] contains: compilation\n" +
		"Atom \"----\" [com.apple.iTunes;MusicBrainz Album Id] contains: abc\n" +
		"Atom \"covr\" contains: 1 piece of artwork\n"
	tags := parseAtoms(out)
	for k, want := range map[string]string{
		"©nam": "Hold It", "aART": "Various Artists",
		"MusicBrainz Album Type": "album; compilation", "MusicBrainz Album Id": "abc",
	} {
		if tags[k] != want {
			t.Errorf("%s = %q, want %q", k, tags[k], want)
		}
	}
}
