// Package adm scrapes AnyDecentMusic's "Just in" chart for newly released
// albums. The site has no API/RSS; it's ASP.NET WebForms HTML that we parse.
package adm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// JustOutURL is the "Just in" chart: new albums added to ADM.
const JustOutURL = "http://www.anydecentmusic.com/chart/JustOut.aspx"

// Release is a single album ADM lists as newly out.
type Release struct {
	// ReviewID is ADM's numeric review id, used as the stable dedupe key.
	ReviewID int
	Artist   string
	Album    string
	// ReviewURL is the absolute ADM review page URL.
	ReviewURL string
	// Added is the date ADM added the album to its chart. Zero if unknown
	// (the JustOut layout does not carry a date; the main chart does).
	Added time.Time
}

// SearchQuery is the string handed to the Tidal search ("Artist Album").
func (r Release) SearchQuery() string {
	return strings.TrimSpace(r.Artist + " " + r.Album)
}

// Client scrapes ADM.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a sensible timeout and browser-like User-Agent.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// JustOut fetches and parses the "Just in" chart.
func (c *Client) JustOut(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, JustOutURL, nil)
	if err != nil {
		return nil, err
	}
	// ADM serves an error/challenge page to default Go UA; mimic a browser.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching JustOut: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JustOut returned HTTP %d", resp.StatusCode)
	}
	return Parse(resp.Body)
}

// Parse extracts releases from JustOut HTML. The page uses two layouts that we
// both parse and merge, deduping by ReviewID:
//
//   - Main list: <p class="title"> with <span class="artist"> + <span
//     class="album">, followed by <p class="quote"><a href="/review/<id>/...">.
//   - Sidebar "New" blocks: <dl class="sidebar_list"> with artist in
//     <dt><strong><a> and album in the following <a>.
func Parse(r io.Reader) ([]Release, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	var releases []Release
	seen := make(map[int]bool)
	add := func(artist, album, href string) {
		artist = strings.TrimSpace(artist)
		album = strings.TrimSpace(album)
		id := reviewID(href)
		if artist == "" || album == "" || id == 0 || seen[id] {
			return
		}
		seen[id] = true
		releases = append(releases, Release{
			ReviewID:  id,
			Artist:    artist,
			Album:     album,
			ReviewURL: absURL(href),
		})
	}

	// Main list: p.title carries artist/album; the sibling p.quote holds the link.
	doc.Find("p.title").Each(func(_ int, title *goquery.Selection) {
		artist := title.Find("span.artist").First().Text()
		album := title.Find("span.album").First().Text()
		href, _ := title.NextAllFiltered("p.quote").First().Find("a[href]").First().Attr("href")
		add(artist, album, href)
	})

	// Sidebar "New" blocks.
	doc.Find("dl.sidebar_list").Each(func(_ int, dl *goquery.Selection) {
		dt := dl.Find("dt").First()
		artist := dt.Find("strong a").First().Text()

		// The album title is the <a> in the <dt> not inside <strong>.
		var album string
		dt.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			if a.Closest("strong").Length() > 0 {
				return true
			}
			album = a.Text()
			return false
		})

		href, _ := dt.Find("a").First().Attr("href")
		add(artist, album, href)
	})

	return releases, nil
}

// HomeURL is the main "Recent Releases" chart, paginated via ?p=N. It spans a
// longer window than JustOut and carries per-album "Added" dates.
const HomeURL = "http://www.anydecentmusic.com/"

// Since walks the paginated main chart backwards in time, collecting every
// album added on or after `since`, deduped by ReviewID. It stops once a page
// yields no album newer than `since` (the chart is ordered so older entries
// appear on later pages), or after maxPages as a safety bound.
func (c *Client) Since(ctx context.Context, since time.Time, maxPages int) ([]Release, error) {
	if maxPages <= 0 {
		maxPages = 60
	}
	seen := make(map[int]bool)
	var out []Release

	for page := 1; page <= maxPages; page++ {
		releases, err := c.chartPage(ctx, page)
		if err != nil {
			return out, fmt.Errorf("page %d: %w", page, err)
		}
		if len(releases) == 0 {
			break // ran past the last page
		}
		anyKept := false
		for _, r := range releases {
			// Entries without a date are kept (can't exclude what we can't date).
			if !r.Added.IsZero() && r.Added.Before(since) {
				continue
			}
			anyKept = true
			if seen[r.ReviewID] {
				continue
			}
			seen[r.ReviewID] = true
			out = append(out, r)
		}
		// If a full page had dates and none were >= since, we've gone too far back.
		if !anyKept {
			break
		}
	}
	return out, nil
}

// chartPage fetches and parses one page of the main chart, retrying a few
// times on transient network errors so one slow page doesn't abort a long walk.
func (c *Client) chartPage(ctx context.Context, page int) ([]Release, error) {
	url := HomeURL
	if page > 1 {
		url = fmt.Sprintf("%s?p=%d", HomeURL, page)
	}

	const attempts = 3
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		releases, err := c.fetchChart(ctx, url)
		if err == nil {
			return releases, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return nil, lastErr
}

func (c *Client) fetchChart(ctx context.Context, url string) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return ParseChart(resp.Body)
}

// ParseChart extracts releases from a main-chart page. Each entry is a
// div.album_info: artist in <h4><a>, album in <h5><a>, and the "Added" date in
// a <p><small>Added: DD/MM/YYYY</small>. The review link supplies the id.
func ParseChart(r io.Reader) ([]Release, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	var releases []Release
	seen := make(map[int]bool)

	doc.Find("div.album_info").Each(func(_ int, info *goquery.Selection) {
		artist := strings.TrimSpace(info.Find("h4 a").First().Text())
		album := strings.TrimSpace(info.Find("h5 a").First().Text())
		href, _ := info.Find("h4 a").First().Attr("href")
		id := reviewID(href)
		if artist == "" || album == "" || id == 0 || seen[id] {
			return
		}
		seen[id] = true

		added := parseAdded(info.Find("small").Text())
		releases = append(releases, Release{
			ReviewID:  id,
			Artist:    artist,
			Album:     album,
			ReviewURL: absURL(href),
			Added:     added,
		})
	})
	return releases, nil
}

// parseAdded extracts the date from text like "Added: 12/06/2026" (DD/MM/YYYY).
// Returns the zero time if no date is present.
func parseAdded(s string) time.Time {
	i := strings.Index(s, "Added:")
	if i < 0 {
		return time.Time{}
	}
	field := strings.TrimSpace(s[i+len("Added:"):])
	// Take just the DD/MM/YYYY token.
	if sp := strings.IndexFunc(field, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' }); sp >= 0 {
		field = field[:sp]
	}
	t, err := time.Parse("02/01/2006", field)
	if err != nil {
		return time.Time{}
	}
	return t
}

// reviewID pulls the numeric id from "/review/14651/The-....aspx".
func reviewID(href string) int {
	parts := strings.Split(strings.Trim(href, "/"), "/")
	for i, p := range parts {
		if p == "review" && i+1 < len(parts) {
			if id, err := strconv.Atoi(parts[i+1]); err == nil {
				return id
			}
		}
	}
	return 0
}

func absURL(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return "http://www.anydecentmusic.com" + href
}
