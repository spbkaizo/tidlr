package genre

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type mapStore map[string][]byte

func (m mapStore) Get(k string) ([]byte, bool) { v, ok := m[k]; return v, ok }
func (m mapStore) Put(k string, v []byte)      { m[k] = v }

func TestTopTagsPersistsOnlyTaggedResults(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Get("artist") == "Nobody" {
			w.Write([]byte(`{"error":6,"message":"The artist you supplied could not be found"}`))
			return
		}
		w.Write([]byte(`{"toptags":{"tag":[{"name":"folk","count":100},{"name":"americana","count":"60"}]}}`))
	}))
	defer srv.Close()

	store := mapStore{}
	client := func(key string) *LastFM {
		c := NewLastFM(key)
		c.BaseURL, c.Interval, c.Store = srv.URL, 1, store
		return c
	}
	ctx := context.Background()
	args := map[string]string{"artist": "Gillian Welch"}

	tags, err := client("key-1").TopTags(ctx, "artist.gettoptags", args)
	if err != nil || len(tags) != 2 || tags[1].Score != 60 {
		t.Fatalf("TopTags = %v, %v", tags, err)
	}
	// A later run (new client, even with a new API key) reads the store.
	again, err := client("key-2").TopTags(ctx, "artist.gettoptags", args)
	if err != nil || len(again) != 2 || hits.Load() != 1 {
		t.Fatalf("second run: %v, %v, %d requests; want cached, 1 request", again, err, hits.Load())
	}

	// Nothing known: not stored, so the next run asks again.
	nobody := map[string]string{"artist": "Nobody"}
	if tags, err := client("key-1").TopTags(ctx, "artist.gettoptags", nobody); err != nil || len(tags) != 0 {
		t.Fatalf("unknown artist: %v, %v", tags, err)
	}
	client("key-1").TopTags(ctx, "artist.gettoptags", nobody)
	if hits.Load() != 3 {
		t.Errorf("unknown artist cached: %d requests, want 3", hits.Load())
	}
	if len(store) != 1 {
		t.Errorf("store holds %d entries, want 1", len(store))
	}
}

// TestTopTagsRetries: Last.fm's "operation failed" (error 8) is retried; an
// invalid key (error 10) is not.
func TestTopTagsRetries(t *testing.T) {
	saved := RetryWaits
	RetryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { RetryWaits = saved }()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch {
		case r.URL.Query().Get("artist") == "BadKey":
			w.Write([]byte(`{"error":10,"message":"Invalid API key"}`))
		case n < 3:
			w.Write([]byte(`{"error":8,"message":"Operation failed - Most likely the backend service failed. Please try again."}`))
		default:
			w.Write([]byte(`{"toptags":{"tag":[{"name":"folk","count":100}]}}`))
		}
	}))
	defer srv.Close()
	c := NewLastFM("k")
	c.BaseURL, c.Interval = srv.URL, 0

	tags, err := c.TopTags(context.Background(), "artist.gettoptags", map[string]string{"artist": "Gillian Welch"})
	if err != nil || len(tags) != 1 || hits.Load() != 3 {
		t.Fatalf("got %v, %v after %d requests; want 1 tag after 3", tags, err, hits.Load())
	}
	hits.Store(10)
	if _, err := c.TopTags(context.Background(), "artist.gettoptags", map[string]string{"artist": "BadKey"}); err == nil || hits.Load() != 11 {
		t.Errorf("bad key: err %v after %d requests; want an error after 1", err, hits.Load()-10)
	}
}
