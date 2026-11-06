// Package genre picks an album's genre grouping (the library folder), genres
// and moods from Last.fm tags, porting the album logic of the lastfmng Picard
// plugin the user's library was built with: weighted toptags from the album,
// its artists and its tracks, translated, then sorted into categories by
// whitelist and a score threshold relative to the category's top tag.
//
// The whitelists and translations are lastfmng's own (GPL), so they are not
// shipped here: Load reads them from the plugin's defaults.ini at run time.
// Without it, Builtin falls back to the ID3v1 genre names for grouping.
package genre

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// Category is one tag category: tags in its whitelist scoring at least
// Threshold times the category's best score, up to Limit of them.
type Category struct {
	Name      string
	Include   map[string]bool
	Exclude   map[string]bool
	Limit     int
	Threshold float64
	Overflow  string // category that receives tags beyond Limit
}

func (c Category) has(tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	return c.Include[tag] && !c.Exclude[tag]
}

// Lists is the configuration the categorizer runs on.
type Lists struct {
	Grouping, Genre, Mood Category
	Translations          map[string]string
	Abbreviations         map[string]bool
}

// Load reads lastfmng's defaults.ini.
func Load(path string) (Lists, error) {
	f, err := os.Open(path)
	if err != nil {
		return Lists{}, err
	}
	defer f.Close()
	return parse(f)
}

// parse reads the INI dialect lastfmng uses: [sections], "key = value",
// indented continuation lines, and ";" or "#" comments.
func parse(r io.Reader) (Lists, error) {
	sections := map[string]map[string]string{}
	var section, key string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"):
			section = strings.Trim(trimmed, "[]")
			if sections[section] == nil {
				sections[section] = map[string]string{}
			}
			key = ""
		case (line[0] == ' ' || line[0] == '\t') && key != "":
			sections[section][key] += " " + trimmed
		default:
			k, v, ok := strings.Cut(trimmed, "=")
			if !ok || section == "" {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(k))
			sections[section][key] = strings.TrimSpace(v)
		}
	}
	if err := sc.Err(); err != nil {
		return Lists{}, err
	}

	defaults := sections["category-defaults"]
	soundtrackIsNoGenre := strings.EqualFold(sections["global"]["soundtrack_is_no_genre"], "true")
	category := func(name string) Category {
		cfg := sections["category-"+name]
		get := func(k string) string {
			if v, ok := cfg[k]; ok {
				return v
			}
			return defaults[k]
		}
		c := Category{Name: name, Include: words(sections["searchlist"][name]), Exclude: map[string]bool{}}
		c.Limit, _ = strconv.Atoi(get("limit"))
		c.Threshold, _ = strconv.ParseFloat(get("threshold"), 64)
		c.Overflow = get("overflow")
		if soundtrackIsNoGenre {
			c.Exclude["soundtrack"] = true
		}
		return c
	}
	l := Lists{
		Grouping:      category("grouping"),
		Genre:         category("genre"),
		Mood:          category("mood"),
		Translations:  map[string]string{},
		Abbreviations: map[string]bool{},
	}
	for from, to := range sections["translations"] {
		l.Translations[from] = strings.ToLower(to)
	}
	for w := range words(sections["searchlist"]["abbreviations"]) {
		l.Abbreviations[strings.ToUpper(w)] = true
	}
	return l, nil
}

// words splits a comma-separated list into a lower-case set.
func words(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Split(s, ",") {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			set[w] = true
		}
	}
	return set
}

// id3Genres is the ID3v1 genre list (with the Winamp extensions), the basis
// lastfmng's grouping list was built on. Used when its defaults.ini is absent.
const id3Genres = `Blues, Classic Rock, Country, Dance, Disco, Funk, Grunge, Hip-Hop, Jazz,
Metal, New Age, Oldies, Other, Pop, R&B, Rap, Reggae, Rock, Techno, Industrial,
Alternative, Ska, Death Metal, Pranks, Soundtrack, Euro-Techno, Ambient,
Trip-Hop, Vocal, Jazz+Funk, Fusion, Trance, Classical, Instrumental, Acid,
House, Game, Sound Clip, Gospel, Noise, Alternative Rock, Bass, Soul, Punk,
Space, Meditative, Instrumental Pop, Instrumental Rock, Ethnic, Gothic,
Darkwave, Techno-Industrial, Electronic, Pop-Folk, Eurodance, Dream,
Southern Rock, Comedy, Cult, Gangsta, Top 40, Christian Rap, Pop/Funk, Jungle,
Native US, Cabaret, New Wave, Psychedelic, Rave, Showtunes, Trailer, Lo-Fi,
Tribal, Acid Punk, Acid Jazz, Polka, Retro, Musical, Rock & Roll, Hard Rock,
Folk, Folk-Rock, National Folk, Swing, Fast Fusion, Bebop, Latin, Revival,
Celtic, Bluegrass, Avantgarde, Gothic Rock, Progressive Rock, Psychedelic Rock,
Symphonic Rock, Slow Rock, Big Band, Chorus, Easy Listening, Acoustic, Humour,
Speech, Chanson, Opera, Chamber Music, Sonata, Symphony, Booty Bass, Primus,
Satire, Slow Jam, Club, Tango, Samba, Folklore, Ballad, Power Ballad,
Rhythmic Soul, Freestyle, Duet, Punk Rock, Drum Solo, A Cappella, Euro-House,
Dance Hall, Goa, Drum & Bass, Club-House, Hardcore, Terror, Indie, BritPop,
Polsk Punk, Beat, Christian Gangsta Rap, Heavy Metal, Black Metal, Crossover,
Contemporary Christian, Christian Rock, Merengue, Salsa, Thrash Metal, Anime,
JPop, Synthpop`

// Builtin is the configuration used without lastfmng's defaults.ini:
// grouping from the ID3v1 genres with lastfmng's limits, no genre or mood
// whitelists (so only the grouping is set).
func Builtin() Lists {
	return Lists{
		Grouping:      Category{Name: "grouping", Include: words(id3Genres), Exclude: map[string]bool{"soundtrack": true}, Limit: 1, Threshold: 0.5, Overflow: "genre"},
		Genre:         Category{Name: "genre", Include: map[string]bool{}, Limit: 4, Threshold: 0.4},
		Mood:          Category{Name: "mood", Include: map[string]bool{}, Limit: 4, Threshold: 0.5},
		Translations:  map[string]string{"hip hop": "hip-hop", "hiphop": "hip-hop", "electronica": "electronic", "electro": "electronic", "dnb": "drum and bass", "drum n bass": "drum and bass", "trip hop": "trip-hop", "lofi": "lo-fi", "lo fi": "lo-fi"},
		Abbreviations: map[string]bool{"USA": true, "UK": true},
	}
}
