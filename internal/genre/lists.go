// Package genre picks an album's genre grouping (the library folder), genres
// and moods from Last.fm tags, porting the album logic of the lastfmng Picard
// plugin: weighted toptags from the album, its artists and its tracks,
// translated, then sorted into categories by whitelist and a score threshold
// relative to the category's top tag.
//
// The whitelists and translations are lastfmng's own (GPL), so they are not
// shipped here: Load reads them from the plugin's defaults.ini at run time,
// for Picard users who have it (FindLastFMNG). Without it, Builtin uses the
// ID3v1 genre names for the grouping and MusicBrainz's genre list (CC0,
// mbgenres.txt) for genres.
package genre

import (
	"bufio"
	_ "embed"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// mbGenres is MusicBrainz's genre list, one per line, from
// https://musicbrainz.org/ws/2/genre/all?fmt=txt (CC0).
//
//go:embed mbgenres.txt
var mbGenres string

// FindLastFMNG returns the path of the lastfmng Picard plugin's defaults.ini
// if it is installed in Picard's usual plugin folder, else "".
func FindLastFMNG() string {
	var dir string
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, "Library", "Preferences")
	} else {
		var err error
		if dir, err = os.UserConfigDir(); err != nil {
			return ""
		}
	}
	path := filepath.Join(dir, "MusicBrainz", "Picard", "plugins", "lastfmng", "defaults.ini")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

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

// moods is the built-in mood whitelist: common Last.fm mood tags.
const moods = `aggressive, angry, atmospheric, beautiful, bittersweet, calm, chill,
chillout, cheerful, dark, dreamy, energetic, epic, euphoric, feel good, fun,
gloomy, groovy, happy, haunting, hypnotic, intense, introspective, lonely,
mellow, melancholic, melancholy, moody, nostalgic, party, peaceful, playful,
powerful, relaxing, romantic, sad, sensual, sexy, smooth, soothing, upbeat,
uplifting, warm`

// builtinTranslations map common spellings of a tag onto one name, so the
// ID3 grouping names and MusicBrainz's genre names meet.
var builtinTranslations = map[string]string{
	"hip hop": "hip-hop", "hiphop": "hip-hop", "trip hop": "trip-hop",
	"electronica": "electronic", "electro": "electronic",
	"dnb": "drum & bass", "drum n bass": "drum & bass", "drum and bass": "drum & bass", "drum'n'bass": "drum & bass",
	"lofi": "lo-fi", "lo fi": "lo-fi",
	"rnb": "r&b", "rhythm and blues": "r&b", "rhythm & blues": "r&b",
	"synth-pop": "synthpop", "synth pop": "synthpop",
	"rock and roll": "rock & roll", "rock n roll": "rock & roll",
	"melancholy": "melancholic", "chillout": "chill",
}

// Builtin is the configuration used without lastfmng's defaults.ini, with
// lastfmng's limits and thresholds: grouping from the ID3v1 genre names,
// genres from MusicBrainz's genre list, moods from a short list of common
// mood tags.
func Builtin() Lists {
	tr := builtinTranslations
	translated := func(set map[string]bool) map[string]bool {
		out := map[string]bool{}
		for w := range set {
			if to, ok := tr[w]; ok {
				w = to
			}
			out[w] = true
		}
		return out
	}
	grouping := translated(words(id3Genres))
	genres := translated(words(strings.ReplaceAll(mbGenres, "\n", ",")))
	for g := range grouping {
		genres[g] = true // a grouping overflowing into genre stays a genre
	}
	return Lists{
		Grouping:      Category{Name: "grouping", Include: grouping, Exclude: map[string]bool{"soundtrack": true}, Limit: 1, Threshold: 0.5, Overflow: "genre"},
		Genre:         Category{Name: "genre", Include: genres, Exclude: map[string]bool{"soundtrack": true}, Limit: 4, Threshold: 0.4},
		Mood:          Category{Name: "mood", Include: translated(words(moods)), Limit: 4, Threshold: 0.5},
		Translations:  tr,
		Abbreviations: map[string]bool{"USA": true, "UK": true, "R&B": true, "EDM": true, "IDM": true, "EBM": true},
	}
}
