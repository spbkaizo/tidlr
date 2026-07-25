// Package tidal is a minimal native Go client for the Tidal API — the subset
// tidlr needs to search for albums and (in a later phase) download tracks. It
// replaces shelling out to the Python `tiddl` tool.
package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// apiURL is the Tidal REST base.
const apiURL = "https://api.tidal.com/v1"

// Rate-limit backoff. Tidal throttles bursty API traffic with HTTP 429 —
// downloading several albums concurrently reliably trips it on
// playbackinfopostpaywall. Waits are randomised across the whole window so
// concurrent workers that were throttled together don't retry in lockstep and
// immediately re-trip the limit.
const (
	rateLimitAttempts = 5
	rateLimitMinWait  = 1 * time.Second
	rateLimitMaxWait  = 180 * time.Second
)

// rateLimitWait picks the delay before the next attempt after a 429. A
// server-sent Retry-After is authoritative and used as a floor (clamped to the
// max wait); otherwise the wait is uniform random over [min, max].
func rateLimitWait(retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > rateLimitMaxWait {
			return rateLimitMaxWait
		}
		return retryAfter
	}
	span := rateLimitMaxWait - rateLimitMinWait
	return rateLimitMinWait + time.Duration(rand.Int63n(int64(span)+1))
}

// parseRetryAfter reads a Retry-After header, which may be delta-seconds or an
// HTTP date. Returns 0 when absent or unparseable.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// Client talks to the Tidal API with a bearer token, transparently refreshing
// on a 401 (mirroring tiddl's TidalClient.fetch behavior) and backing off on a
// 429.
type Client struct {
	HTTP *http.Client
	Auth *Auth
	// Log, when set, reports rate-limit waits so a long stall isn't silent.
	Log *log.Logger
}

// New returns a Client using the given auth and a default HTTP client.
func New(auth *Auth) *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second},
		Auth: auth,
		Log:  log.Default(),
	}
}

// countryCode returns the account country, defaulting sensibly.
func (c *Client) countryCode() string {
	if c.Auth.CountryCode != "" {
		return c.Auth.CountryCode
	}
	return "US"
}

// get issues GET /{path} with query params and decodes the JSON into out. On a
// 401 it refreshes the token once and retries; on a 429 it waits (honouring
// Retry-After, else a random 1-180s) and retries, so transient Tidal rate
// limiting doesn't abort a whole album.
func (c *Client) get(ctx context.Context, path string, params map[string]string, out any) error {
	return c.getURL(ctx, apiURL+"/"+path, params, out)
}

// getURL is get against a fully-qualified base URL, holding the 429 retry loop.
// Split out from get so tests can target a local server.
func (c *Client) getURL(ctx context.Context, base string, params map[string]string, out any) error {
	var lastErr error
	for attempt := 1; attempt <= rateLimitAttempts; attempt++ {
		err := c.getWithRetry(ctx, base, params, out, true)
		if err == nil {
			return nil
		}
		var rl rateLimitedError
		if !errors.As(err, &rl) {
			return err
		}
		lastErr = err
		if attempt == rateLimitAttempts {
			break
		}

		wait := rateLimitWait(rl.RetryAfter)
		if c.Log != nil {
			c.Log.Printf("rate limited on %s, waiting %s before retry %d/%d",
				rl.Path, wait.Round(time.Second), attempt+1, rateLimitAttempts)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

// rateLimitedError marks an HTTP 429 so get can distinguish "slow down and
// retry" from a genuine failure.
type rateLimitedError struct {
	Path       string
	RetryAfter time.Duration
}

func (e rateLimitedError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d (rate limited)", e.Path, http.StatusTooManyRequests)
}

func (c *Client) getWithRetry(ctx context.Context, base string, params map[string]string, out any, allowRefresh bool) error {
	// Label errors with the endpoint, not the full URL with credentials-ish query.
	path := strings.TrimPrefix(base, apiURL+"/")
	u := base
	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Auth.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && allowRefresh {
		if err := c.Auth.Refresh(ctx, c.HTTP); err != nil {
			return fmt.Errorf("token expired and refresh failed: %w", err)
		}
		return c.getWithRetry(ctx, base, params, out, false)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return rateLimitedError{
			Path:       path,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// userID returns the account user id as a string (tiddl stores it as a number).
func (c *Client) userID() string {
	switch v := c.Auth.UserID.(type) {
	case string:
		return v
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%.0f", v), ".0")
	default:
		return fmt.Sprintf("%v", v)
	}
}
