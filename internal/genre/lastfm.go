package genre

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LastFMURL is the Last.fm API endpoint.
const LastFMURL = "https://ws.audioscrobbler.com/2.0/"

// LastFM fetches toptags from Last.fm, caching each query for the life of the
// value (an album asks for the same artist's tags once per track).
type LastFM struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	// Interval is the minimum gap between requests (lastfmng used 200ms).
	Interval time.Duration

	// Store, if set, keeps tag lookups between runs. Only lookups that
	// returned tags are stored: an album, artist or track Last.fm knows
	// nothing about today may be tagged later, so those are asked again.
	Store Store

	mu    sync.Mutex
	last  time.Time
	cache map[string][]Tag
}

// Store is a persistent key-value cache.
type Store interface {
	Get(key string) ([]byte, bool)
	Put(key string, value []byte)
}

// NewLastFM returns a client for the public API.
func NewLastFM(apiKey string) *LastFM {
	return &LastFM{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: LastFMURL, APIKey: apiKey, Interval: 200 * time.Millisecond}
}

// TopTags returns the raw toptags for a Last.fm method ("album.gettoptags",
// "artist.gettoptags", "track.gettoptags") and its arguments. An unknown
// album, artist or track yields no tags rather than an error.
func (c *LastFM) TopTags(ctx context.Context, method string, args map[string]string) ([]Tag, error) {
	params := url.Values{"method": {method}}
	for k, v := range args {
		params.Set(k, v)
	}
	// The cache key leaves out the API key, so changing keys keeps the cache.
	key := params.Encode()
	params.Set("api_key", c.APIKey)
	params.Set("format", "json")
	params.Set("autocorrect", "0")

	c.mu.Lock()
	defer c.mu.Unlock()
	if tags, ok := c.cache[key]; ok {
		return tags, nil
	}
	if c.Store != nil {
		if b, ok := c.Store.Get(key); ok {
			var tags []Tag
			if json.Unmarshal(b, &tags) == nil {
				c.remember(key, tags)
				return tags, nil
			}
		}
	}
	if d := time.Until(c.last.Add(c.Interval)); d > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	c.last = time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// The URL in a transport error carries the API key: don't log it.
		return nil, fmt.Errorf("last.fm %s: %s", method, redactKey(err.Error(), c.APIKey))
	}
	defer resp.Body.Close()
	var body struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
		TopTags struct {
			Tag []struct {
				Name  string          `json:"name"`
				Count json.RawMessage `json:"count"`
			} `json:"tag"`
		} `json:"toptags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("last.fm %s: HTTP %d: %w", method, resp.StatusCode, err)
	}
	switch body.Error {
	case 0:
	case 6: // album/artist/track not found
		body.TopTags.Tag = nil
	default:
		return nil, fmt.Errorf("last.fm %s: error %d: %s", method, body.Error, body.Message)
	}
	var tags []Tag
	for _, t := range body.TopTags.Tag {
		// count is a number in JSON, though older responses quote it.
		n, err := strconv.Atoi(string(trimQuotes(t.Count)))
		if err != nil || n < 1 {
			continue
		}
		tags = append(tags, Tag{Name: t.Name, Score: float64(n)})
	}
	c.remember(key, tags)
	if c.Store != nil && len(tags) > 0 {
		if b, err := json.Marshal(tags); err == nil {
			c.Store.Put(key, b)
		}
	}
	return tags, nil
}

func (c *LastFM) remember(key string, tags []Tag) {
	if c.cache == nil {
		c.cache = map[string][]Tag{}
	}
	c.cache[key] = tags
}

func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "<api_key>")
}

func trimQuotes(b []byte) []byte {
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}

// Album is what genre needs to know about an album.
type Album struct {
	AlbumArtist string
	Title       string
	Tracks      []Track
}

// Track is one track's artist and title.
type Track struct {
	Artist string
	Title  string
}

// ForAlbum gathers an album's Last.fm tags the way lastfmng does — the album's
// own tags, then each track's artist tags and each track's tags — weighs and
// merges them, and categorizes the result.
func (l Lists) ForAlbum(ctx context.Context, fm *LastFM, a Album) (Result, error) {
	n := float64(len(a.Tracks))
	if n == 0 {
		n = 1
	}
	albumTags, err := fm.TopTags(ctx, "album.gettoptags", map[string]string{"artist": a.AlbumArtist, "album": a.Title})
	if err != nil {
		return Result{}, err
	}
	lists := [][]Tag{weigh(l.translateAll(albumTags), weightAlbum*n)}
	for _, t := range a.Tracks {
		artistTags, err := fm.TopTags(ctx, "artist.gettoptags", map[string]string{"artist": t.Artist})
		if err != nil {
			return Result{}, err
		}
		trackTags, err := fm.TopTags(ctx, "track.gettoptags", map[string]string{"artist": t.Artist, "track": t.Title})
		if err != nil {
			return Result{}, err
		}
		lists = append(lists, weigh(l.translateAll(artistTags), weightArtist), weigh(l.translateAll(trackTags), weightTrack))
	}
	return l.Categorize(Merge(lists...)), nil
}

// FromTags categorizes tags from another source (MusicBrainz genres, with
// their vote counts as scores), for when Last.fm has nothing.
func (l Lists) FromTags(tags []Tag) Result {
	return l.Categorize(Merge(l.translateAll(tags)))
}

func (l Lists) translateAll(tags []Tag) []Tag {
	out := make([]Tag, len(tags))
	for i, t := range tags {
		out[i] = Tag{l.translate(t.Name), t.Score}
	}
	return out
}
