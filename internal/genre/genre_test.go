package genre

import (
	"reflect"
	"strings"
	"testing"
)

// iniSample follows the layout of lastfmng's defaults.ini (abridged).
const iniSample = `[global]
soundtrack_is_no_genre = True

[category-defaults]
; a hard limit for how many tags are assigned to the metatag
limit = 4
threshold = 0.5

[category-grouping]
limit = 1
overflow = genre

[category-genre]
limit = 4
threshold = 0.4

[category-mood]
limit = 4

[searchlist]
grouping = Hip-Hop, Rap, Rock, Electronic,
           Indie, R&B, Drum & Bass, Soundtrack
genre = rap, west coast, jazz rap, conscious hip hop, soundtrack
mood = funky, chill, angry
abbreviations = USA, UK

[translations]
hip hop = hip-hop
electronica = electronic
`

func TestParse(t *testing.T) {
	l, err := parse(strings.NewReader(iniSample))
	if err != nil {
		t.Fatal(err)
	}
	if l.Grouping.Limit != 1 || l.Grouping.Threshold != 0.5 || l.Grouping.Overflow != "genre" {
		t.Errorf("grouping = %+v", l.Grouping)
	}
	if l.Genre.Limit != 4 || l.Genre.Threshold != 0.4 {
		t.Errorf("genre = %+v", l.Genre)
	}
	if !l.Grouping.has("Drum & Bass") || !l.Grouping.has("indie") {
		t.Error("continuation lines not joined into the grouping list")
	}
	if l.Grouping.has("soundtrack") {
		t.Error("soundtrack_is_no_genre should exclude soundtrack")
	}
	if l.translate("Hip Hop") != "hip-hop" {
		t.Errorf("translate(Hip Hop) = %q", l.translate("Hip Hop"))
	}
}

func TestCategorize(t *testing.T) {
	l, _ := parse(strings.NewReader(iniSample))
	tags := Merge(
		weigh(l.translateAll([]Tag{{"Hip Hop", 100}, {"rap", 80}, {"west coast", 40}, {"funky", 30}}), 10),
		weigh(l.translateAll([]Tag{{"hip-hop", 100}, {"jazz rap", 50}, {"rock", 5}, {"funky", 20}}), 5),
	)
	got := l.Categorize(tags)
	// jazz rap (250) falls below 40% of rap (800), the genre threshold.
	want := Result{Grouping: "Hip-Hop", Genres: []string{"Rap", "West Coast"}, Moods: []string{"Funky"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Categorize = %+v, want %+v", got, want)
	}
}

func TestCategorizeOverflowAndThreshold(t *testing.T) {
	l, _ := parse(strings.NewReader(iniSample))
	// Indie wins grouping; Rock (above half of Indie) overflows into genre;
	// Electronic (below half) is dropped.
	got := l.Categorize(Merge([]Tag{{"indie", 100}, {"rock", 60}, {"electronic", 40}, {"rap", 50}}))
	if got.Grouping != "Indie" {
		t.Errorf("grouping = %q", got.Grouping)
	}
	if want := []string{"Rock", "Rap"}; !reflect.DeepEqual(got.Genres, want) {
		t.Errorf("genres = %q, want %q", got.Genres, want)
	}
	// rap is both a grouping and a genre: it overflows and matches directly,
	// but is listed once.
	if got := l.Categorize(Merge([]Tag{{"hip-hop", 100}, {"rap", 90}})); !reflect.DeepEqual(got.Genres, []string{"Rap"}) {
		t.Errorf("duplicate overflow: genres = %q", got.Genres)
	}
	if got := l.Categorize(nil); got.Grouping != "" || len(got.Genres) != 0 {
		t.Errorf("no tags gave %+v", got)
	}
}

func TestTitleCase(t *testing.T) {
	l, _ := parse(strings.NewReader(iniSample))
	// The forms the user's genre folders actually use.
	for in, want := range map[string]string{
		"hip-hop": "Hip-Hop", "lo-fi": "Lo-Fi", "drum & bass": "Drum & Bass", "r&b": "R&b",
		"singer-songwriter": "Singer-Songwriter", "music of the uk": "Music of the UK", "trip-hop": "Trip-Hop",
	} {
		if got := l.titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuiltin runs Last.fm-style tags (a Fleet Foxes album's, roughly)
// through the built-in lists: an ID3 grouping, MusicBrainz genres, moods.
func TestBuiltin(t *testing.T) {
	l := Builtin()
	if len(l.Genre.Include) < 1000 {
		t.Fatalf("built-in genre list has %d genres; the MusicBrainz list was not embedded", len(l.Genre.Include))
	}
	tags := Merge(l.translateAll([]Tag{
		{"folk", 100}, {"indie folk", 80}, {"indie", 60}, {"seen live", 55},
		{"chamber pop", 45}, {"mellow", 40}, {"Hip Hop", 10}, {"soundtrack", 90},
	}))
	got := l.Categorize(tags)
	// "indie" overflows from the grouping to the front of genre, as in lastfmng.
	want := Result{Grouping: "Folk", Genres: []string{"Indie", "Folk", "Indie Folk", "Chamber Pop"}, Moods: []string{"Mellow"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Categorize = %+v, want %+v", got, want)
	}
	if got := l.Categorize(Merge(l.translateAll([]Tag{{"rnb", 100}}))); got.Grouping != "R&B" {
		t.Errorf("rnb grouping = %q, want R&B", got.Grouping)
	}
	// MusicBrainz's spellings meet the ID3 grouping names.
	for _, g := range []string{"hip hop", "drum and bass", "trip hop", "synth-pop"} {
		if tr := l.translate(g); !l.Grouping.has(tr) {
			t.Errorf("%q translates to %q, not a grouping", g, tr)
		}
	}
}
