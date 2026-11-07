package filing

import "testing"

// Cases taken from albums Picard filed in the user's library; the expected
// paths are the real ones on disk.
func TestPathGenre(t *testing.T) {
	cases := []struct {
		name  string
		album Album
		track Track
		want  string
	}{
		{
			"plain album; trailing dot on the album folder becomes _",
			Album{AlbumArtist: "Kendrick Lamar", Title: "DAMN.", Grouping: "Hip-Hop", ReleaseTypes: []string{"album"}, OriginalYear: "2017", TotalDiscs: 1},
			Track{Disc: 1, Number: 1, Title: "BLOOD."},
			"Archive Albums/Hip-Hop/Kendrick Lamar - 2017 - DAMN_/01 - BLOOD..m4a",
		},
		{
			"? becomes ~ in folders and files; a file keeps its trailing dots",
			Album{AlbumArtist: "Public Enemy", Title: "What You Gonna Do When the Grid Goes Down?", Grouping: "Hip-Hop", ReleaseTypes: []string{"album"}, OriginalDate: "2020-09-25", TotalDiscs: 1},
			Track{Disc: 1, Number: 1, Title: "When the Grid Go Down...."},
			"Archive Albums/Hip-Hop/Public Enemy - 2020 - What You Gonna Do When the Grid Goes Down~/01 - When the Grid Go Down.....m4a",
		},
		{
			"... in the album title becomes …",
			Album{AlbumArtist: "Sinéad O'Connor", Title: "So Far... The Best of Sinéad O'Connor", Grouping: "Alternative", ReleaseTypes: []string{"album", "compilation"}, OriginalYear: "1997", TotalDiscs: 1},
			Track{Disc: 1, Number: 1, Title: "Nothing Compares 2 U"},
			"Archive Albums/Alternative/Sinéad O'Connor - 1997 - So Far… The Best of Sinéad O'Connor/01 - Nothing Compares 2 U.m4a",
		},
		{
			"multi-disc prefixes the disc number",
			Album{AlbumArtist: "Bob Dylan", Title: "Rough and Rowdy Ways", Grouping: "Folk", ReleaseTypes: []string{"album"}, OriginalYear: "2020", TotalDiscs: 2},
			Track{Disc: 2, Number: 1, Title: "Murder Most Foul"},
			"Archive Albums/Folk/Bob Dylan - 2020 - Rough and Rowdy Ways/201 - Murder Most Foul.m4a",
		},
		{
			"Various Artists compilation; : becomes ~",
			Album{AlbumArtist: "Various Artists", Title: "Future Disco: Technicolour Nights", Grouping: "Electronic", ReleaseTypes: []string{"album", "compilation"}, OriginalYear: "2019", TotalDiscs: 1},
			Track{Disc: 1, Number: 1, Title: "Hold It"},
			"Archive Compilations/Electronic/Various Artists - 2019 - Future Disco~ Technicolour Nights/01 - Hold It.m4a",
		},
		{
			"soundtrack, even by Various Artists",
			Album{AlbumArtist: "Various Artists", Title: "The Matrix", Grouping: "Electronic", ReleaseTypes: []string{"album", "soundtrack"}, OriginalYear: "1999", TotalDiscs: 1},
			Track{Disc: 1, Number: 13, Title: "Wake Up"},
			"Archive Soundtracks/Electronic/Various Artists - 1999 - The Matrix/13 - Wake Up.m4a",
		},
		{
			"no grouping files under Unknown; no date gives 0000; / in a value becomes _",
			Album{AlbumArtist: "AC/DC", Title: "Live", ReleaseTypes: []string{"album"}, TotalDiscs: 1},
			Track{Disc: 1, Number: 3, Title: "Either/Or"},
			"Archive Albums/Unknown/AC_DC - 0000 - Live/03 - Either_Or.m4a",
		},
	}
	for _, tc := range cases {
		if got := LayoutGenre.Path("", tc.album, tc.track, ".m4a"); got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		}
	}
}

func TestPathArtist(t *testing.T) {
	cases := []struct {
		album Album
		track Track
		want  string
	}{
		{
			Album{AlbumArtist: "Kendrick Lamar", Title: "DAMN.", Grouping: "Hip-Hop", OriginalYear: "2017", TotalDiscs: 1},
			Track{Disc: 1, Number: 1, Title: "BLOOD."},
			"Kendrick Lamar/2017 - DAMN_/01 - BLOOD..m4a",
		},
		{
			Album{AlbumArtist: "Various Artists", Title: "The Matrix", ReleaseTypes: []string{"album", "soundtrack"}, OriginalYear: "1999", TotalDiscs: 1},
			Track{Disc: 1, Number: 13, Title: "Wake Up"},
			"Various Artists/1999 - The Matrix/13 - Wake Up.m4a",
		},
		{
			Album{Artist: "AC/DC", Title: "Live: 1992", TotalDiscs: 2},
			Track{Disc: 2, Number: 3, Title: "Either/Or"},
			"AC_DC/0000 - Live~ 1992/203 - Either_Or.m4a",
		},
	}
	for _, tc := range cases {
		if got := LayoutArtist.Path("", tc.album, tc.track, ".m4a"); got != tc.want {
			t.Errorf("got  %s\nwant %s", got, tc.want)
		}
	}
}

func TestParseLayout(t *testing.T) {
	for in, want := range map[string]Layout{"": LayoutArtist, "artist": LayoutArtist, " Genre ": LayoutGenre} {
		if got, err := ParseLayout(in); err != nil || got != want {
			t.Errorf("ParseLayout(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLayout("decade"); err == nil {
		t.Error("ParseLayout(decade) should fail")
	}
}
