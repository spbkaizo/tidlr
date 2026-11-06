// Package mbtest replays recorded MusicBrainz responses in tests.
package mbtest

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// FixtureName is the file a response to url is recorded under.
func FixtureName(url string) string {
	sum := sha1.Sum([]byte(url))
	return hex.EncodeToString(sum[:8]) + ".json"
}

// Replay serves responses recorded in Dir, keyed by FixtureName of the request
// URL. A file starting "404" replays a not-found. A missing recording fails
// the test with "MISSING <name> <url>", so it can be fetched and saved there.
type Replay struct {
	Dir string
	T   testing.TB
}

// RoundTrip implements http.RoundTripper.
func (r Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	body, err := os.ReadFile(filepath.Join(r.Dir, FixtureName(url)))
	if os.IsNotExist(err) {
		r.T.Errorf("MISSING %s %s", FixtureName(url), url)
		return nil, fmt.Errorf("no recording for %s", url)
	}
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if bytes.HasPrefix(body, []byte("404")) {
		status, body = http.StatusNotFound, nil
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: req}, nil
}
