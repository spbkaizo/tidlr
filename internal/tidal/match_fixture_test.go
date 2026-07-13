package tidal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fixtureServer serves the recorded Tidal JSON so the two-stage Match logic can
// be exercised offline. /search returns the search fixture; the artist albums
// endpoint returns the artist_albums fixture on the first page and an empty page
// afterwards.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	search, err := os.ReadFile("testdata/search_olivia.json")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}
	albums, err := os.ReadFile("testdata/artist_albums_olivia.json")
	if err != nil {
		t.Fatalf("read artist albums fixture: %v", err)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/search"):
			w.Write(search)
		case strings.Contains(r.URL.Path, "/artists/") && strings.HasSuffix(r.URL.Path, "/albums"):
			if r.URL.Query().Get("offset") == "0" {
				w.Write(albums)
			} else {
				w.Write([]byte(`{"limit":10,"offset":10,"totalNumberOfItems":24,"items":[]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

// newTestClient points a Client at the fixture server by overriding apiURL via
// a custom transport that rewrites the host.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New(&Auth{Token: "test", CountryCode: "US"})
	c.HTTP = srv.Client()
	c.HTTP.Transport = rewriteHost{base: srv.URL, rt: srv.Client().Transport}
	return c
}

// rewriteHost redirects requests aimed at api.tidal.com to the test server.
type rewriteHost struct {
	base string
	rt   http.RoundTripper
}

func (rw rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace scheme+host with the test server's, keep path+query.
	newURL := rw.base + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	r2 := req.Clone(req.Context())
	u, _ := req.URL.Parse(newURL)
	r2.URL = u
	r2.Host = u.Host
	rt := rw.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(r2)
}

func TestMatchUpgradesAtmosToStereo(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()

	m := &Matcher{Client: newTestClient(t, srv)}
	got, err := m.Match(context.Background(), "Olivia Rodrigo", "You Seem Pretty Sad for a Girl So in Love")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}

	// Search's top hit is the Atmos edition 533158651; the artist-catalogue
	// stage must upgrade to a lossless STEREO explicit edition of the same title.
	if got.FromSearchID != 533158651 {
		t.Errorf("FromSearchID = %d, want 533158651 (the Atmos hit)", got.FromSearchID)
	}
	if !got.Upgraded {
		t.Errorf("expected an edition upgrade, got id %d unchanged", got.ID)
	}
	if !got.Lossless {
		t.Errorf("chosen edition should be lossless, got mode=%s quality path", got.Mode)
	}
	if got.Mode != "STEREO" {
		t.Errorf("chosen mode = %q, want STEREO", got.Mode)
	}
	// The explicit lossless stereo edition the user identified.
	if got.ID != 532396298 {
		t.Errorf("chosen id = %d, want 532396298 (explicit lossless stereo)", got.ID)
	}
}
