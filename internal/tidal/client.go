// Package tidal is a minimal native Go client for the Tidal API — the subset
// tidlr needs to search for albums and (in a later phase) download tracks. It
// replaces shelling out to the Python `tiddl` tool.
package tidal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiURL is the Tidal REST base.
const apiURL = "https://api.tidal.com/v1"

// Client talks to the Tidal API with a bearer token, transparently refreshing
// on a 401 (mirroring tiddl's TidalClient.fetch behavior).
type Client struct {
	HTTP *http.Client
	Auth *Auth
}

// New returns a Client using the given auth and a default HTTP client.
func New(auth *Auth) *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second},
		Auth: auth,
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
// 401 it refreshes the token once and retries.
func (c *Client) get(ctx context.Context, path string, params map[string]string, out any) error {
	return c.getWithRetry(ctx, path, params, out, true)
}

func (c *Client) getWithRetry(ctx context.Context, path string, params map[string]string, out any, allowRefresh bool) error {
	u := apiURL + "/" + path
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
		return c.getWithRetry(ctx, path, params, out, false)
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
