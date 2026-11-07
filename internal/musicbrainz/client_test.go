package musicbrainz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetRetriesThenReportsUnavailable: 503s and 429s are retried, and a
// service that never recovers is ErrUnavailable, not an answer.
func TestGetRetriesThenReportsUnavailable(t *testing.T) {
	saved := RetryWaits
	RetryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { RetryWaits = saved }()

	var hits atomic.Int32
	var ok atomic.Bool // the service recovers
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if ok.Load() && n >= 3 {
			w.Write([]byte(`{"id":"r1","title":"T"}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("tidlr", "test")
	c.BaseURL, c.Interval = srv.URL, 1

	if _, err := c.LookupRelease(context.Background(), "r1"); !errors.Is(err, ErrUnavailable) || hits.Load() != 3 {
		t.Errorf("err = %v after %d requests; want ErrUnavailable after 3", err, hits.Load())
	}
	hits.Store(0)
	ok.Store(true)
	if r, err := c.LookupRelease(context.Background(), "r1"); err != nil || r.ID != "r1" {
		t.Errorf("recovering service: %+v, %v", r, err)
	}
}
