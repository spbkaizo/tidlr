// Package musicbrainz identifies downloaded albums on MusicBrainz and fetches
// the release data Picard would tag them with. It talks to the public web
// service (https://musicbrainz.org/ws/2/), which allows one request a second
// per client and requires a descriptive User-Agent.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// DefaultBaseURL is the public MusicBrainz web service.
const DefaultBaseURL = "https://musicbrainz.org/ws/2"

// Client is a rate-limited MusicBrainz web service client.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string // MusicBrainz rejects clients without a meaningful one
	// Interval is the minimum gap between requests (default 1s, the
	// service's limit for anonymous clients).
	Interval time.Duration

	mu   sync.Mutex
	last time.Time
}

// New returns a client for the public service, identifying as app/version.
func New(app, version string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		BaseURL:   DefaultBaseURL,
		UserAgent: fmt.Sprintf("%s/%s ( https://github.com/spbkaizo/tidlr )", app, version),
		Interval:  time.Second,
	}
}

// wait blocks until the next request may be sent under the rate limit.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	interval := c.Interval
	if interval == 0 {
		interval = time.Second
	}
	if d := time.Until(c.last.Add(interval)); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	c.last = time.Now()
	return nil
}

// get fetches path?params as JSON into out. It retries 503 (the service's
// "slow down" signal) and transient network errors with growing backoff.
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("fmt", "json")
	u := c.BaseURL + "/" + path + "?" + params.Encode()

	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		retry, err := c.do(ctx, u, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			return err
		}
		if attempt == len(RetryWaits) {
			return fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(RetryWaits[attempt]):
		}
	}
}

// RetryWaits are the pauses before retrying a request that failed in a way
// worth trying again (network errors, 5xx, 429): about four minutes in all,
// enough to ride out MusicBrainz's usual overload spells.
var RetryWaits = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second}

// ErrUnavailable means MusicBrainz could not be reached, or kept failing,
// through every retry: an outage, not an answer about the album.
var ErrUnavailable = errors.New("musicbrainz unavailable")

// do performs one request; retry reports whether a failure is worth retrying.
func (c *Client) do(ctx context.Context, u string, out any) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return false, json.NewDecoder(resp.Body).Decode(out)
	case resp.StatusCode == http.StatusNotFound:
		return false, ErrNotFound
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		io.Copy(io.Discard, resp.Body)
		return true, fmt.Errorf("musicbrainz: HTTP %d", resp.StatusCode)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("musicbrainz: HTTP %d: %s", resp.StatusCode, body)
	}
}

// ErrNotFound is returned for an unknown id.
var ErrNotFound = fmt.Errorf("musicbrainz: not found")

// SearchReleases runs a release search (Lucene syntax) and returns up to limit
// hits, best first.
func (c *Client) SearchReleases(ctx context.Context, query string, limit int) ([]Release, error) {
	var res struct {
		Releases []Release `json:"releases"`
	}
	err := c.get(ctx, "release", url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}, &res)
	return res.Releases, err
}

// ReleaseIDsByISRC returns the ids of every release containing a recording
// with this ISRC.
func (c *Client) ReleaseIDsByISRC(ctx context.Context, isrc string) ([]string, error) {
	var res struct {
		Recordings []struct {
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	err := c.get(ctx, "isrc/"+url.PathEscape(isrc), url.Values{"inc": {"releases"}}, &res)
	if err == ErrNotFound {
		return nil, nil
	}
	var ids []string
	for _, rec := range res.Recordings {
		for _, r := range rec.Releases {
			ids = append(ids, r.ID)
		}
	}
	return ids, err
}

// releaseInc is everything tagging needs from a release lookup.
const releaseInc = "artists artist-credits labels recordings release-groups media isrcs genres"

// LookupRelease fetches a release with its tracks, credits, labels and
// release group.
func (c *Client) LookupRelease(ctx context.Context, id string) (Release, error) {
	var r Release
	err := c.get(ctx, "release/"+url.PathEscape(id), url.Values{"inc": {releaseInc}}, &r)
	return r, err
}

// browseInc is what fitting and ranking a release needs from a browse: track
// lengths and ISRCs, and the country and format prefs compare.
const browseInc = "artist-credits recordings media isrcs"

// ReleasesInGroup returns every release in a release group with its tracks,
// paging through the browse endpoint.
func (c *Client) ReleasesInGroup(ctx context.Context, groupID string) ([]Release, error) {
	var out []Release
	for offset := 0; ; {
		var res struct {
			Count    int       `json:"release-count"`
			Releases []Release `json:"releases"`
		}
		err := c.get(ctx, "release", url.Values{
			"release-group": {groupID}, "inc": {browseInc},
			"limit": {"100"}, "offset": {strconv.Itoa(offset)},
		}, &res)
		if err != nil {
			return out, err
		}
		out = append(out, res.Releases...)
		offset += len(res.Releases)
		if len(res.Releases) == 0 || offset >= res.Count {
			return out, nil
		}
	}
}
