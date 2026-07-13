package tidal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// authURL is Tidal's OAuth2 base.
const authURL = "https://auth.tidal.com/v1/oauth2"

// clientCreds is the (base64-obfuscated) client id/secret pair, matching the
// blob tiddl uses so the same stored refresh token works. Format: "id;secret".
const clientCredsB64 = "NE4zbjZRMXg5NUxMNUs3cDtvS09YZkpXMzcxY1g2eGFaMFB5aGdHTkJkTkxsQlpkNEFLS1lvdWdNamlrPQ=="

func clientCreds() (id, secret string, err error) {
	raw, err := base64.StdEncoding.DecodeString(clientCredsB64)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(raw), ";", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed client credentials")
	}
	return parts[0], parts[1], nil
}

// Auth is the persisted Tidal session, matching tiddl's ~/.tiddl/auth.json so an
// existing tiddl login is reused without re-authenticating.
type Auth struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
	UserID       any    `json:"user_id"`    // tiddl stores as number or string
	CountryCode  string `json:"country_code"`

	path string // where this was loaded from, for SaveAuth
}

// NeedsRefresh reports whether the access token has expired or is close enough
// to expiry (within 5 minutes) that it should be refreshed before a batch.
func (a *Auth) NeedsRefresh() bool {
	if a.ExpiresAt == 0 {
		return true // unknown expiry: refresh to be safe
	}
	return time.Now().Unix() >= a.ExpiresAt-300
}

// DefaultAuthPath returns tiddl's auth file location (~/.tiddl/auth.json).
func DefaultAuthPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tiddl", "auth.json"), nil
}

// LoadAuth reads the token store from path (or DefaultAuthPath if empty).
func LoadAuth(path string) (*Auth, error) {
	if path == "" {
		p, err := DefaultAuthPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading auth %s: %w (run `tidlr login`)", path, err)
	}
	var a Auth
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("parsing auth %s: %w", path, err)
	}
	if a.Token == "" {
		return nil, fmt.Errorf("no token in %s (run `tidlr login`)", path)
	}
	a.path = path
	return &a, nil
}

// Save persists the current token state back to the file it was loaded from.
func (a *Auth) Save() error {
	if a.path == "" {
		return fmt.Errorf("auth has no path to save to")
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.path, b, 0o600)
}

// authResponse is the token endpoint reply.
type authResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// deviceAuthResponse is the device_authorization reply.
type deviceAuthResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// loginResponse is the successful device-flow token grant.
type loginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	UserID       int64  `json:"user_id"`
	User         struct {
		CountryCode string `json:"countryCode"`
	} `json:"user"`
}

// Login runs the OAuth2 device-authorization flow: it prints a verification URL
// for the user to approve, polls until granted (or expiry), and writes the token
// store to path (or DefaultAuthPath if empty). prompt is called once with the
// URL the user must visit.
func Login(ctx context.Context, httpc *http.Client, path string, prompt func(url string)) (*Auth, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	id, secret, err := clientCreds()
	if err != nil {
		return nil, err
	}

	// 1. Request a device code.
	dev, err := requestDeviceAuth(ctx, httpc, id)
	if err != nil {
		return nil, err
	}
	url := dev.VerificationURIComplete
	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}
	if prompt != nil {
		prompt(url)
	}

	// 2. Poll the token endpoint until the user approves.
	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	interval := time.Duration(dev.Interval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device authorization expired before approval")
		}

		lr, pending, err := pollToken(ctx, httpc, id, secret, dev.DeviceCode)
		if pending {
			continue
		}
		if err != nil {
			return nil, err
		}

		if path == "" {
			if p, err := DefaultAuthPath(); err == nil {
				path = p
			}
		}
		a := &Auth{
			Token:        lr.AccessToken,
			RefreshToken: lr.RefreshToken,
			ExpiresAt:    time.Now().Unix() + lr.ExpiresIn,
			UserID:       lr.UserID,
			CountryCode:  lr.User.CountryCode,
			path:         path,
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := a.Save(); err != nil {
			return nil, err
		}
		return a, nil
	}
}

func requestDeviceAuth(ctx context.Context, httpc *http.Client, clientID string) (*deviceAuthResponse, error) {
	form := url.Values{
		"client_id": {clientID},
		"scope":     {"r_usr+w_usr+w_sub"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/device_authorization", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device authorization failed: HTTP %d", resp.StatusCode)
	}
	var dev deviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
		return nil, err
	}
	return &dev, nil
}

// pollToken tries once to exchange the device code for a token. It returns
// pending=true while the user has not yet approved.
func pollToken(ctx context.Context, httpc *http.Client, clientID, clientSecret, deviceCode string) (*loginResponse, bool, error) {
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"scope":       {"r_usr+w_usr+w_sub"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var lr loginResponse
		if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
			return nil, false, err
		}
		return &lr, false, nil
	}

	// Non-200: distinguish "still waiting" from a real error.
	var e struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if e.Error == "authorization_pending" {
		return nil, true, nil
	}
	return nil, false, fmt.Errorf("device token poll failed: %s (HTTP %d)", e.Error, resp.StatusCode)
}

// Refresh exchanges the refresh token for a fresh access token and persists it.
func (a *Auth) Refresh(ctx context.Context, httpc *http.Client) error {
	if a.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}
	id, secret, err := clientCreds()
	if err != nil {
		return err
	}

	form := url.Values{
		"client_id":     {id},
		"refresh_token": {a.RefreshToken},
		"grant_type":    {"refresh_token"},
		"scope":         {"r_usr+w_usr+w_sub"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(id, secret)

	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token refresh failed: HTTP %d", resp.StatusCode)
	}
	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return err
	}
	a.Token = ar.AccessToken
	a.ExpiresAt = time.Now().Unix() + ar.ExpiresIn
	return a.Save()
}
