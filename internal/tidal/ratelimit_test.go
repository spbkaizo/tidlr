package tidal

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "42", 42 * time.Second},
		{"seconds padded", "  7 ", 7 * time.Second},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"garbage", "soon", 0},
		{"past http date", "Mon, 02 Jan 2006 15:04:05 GMT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.in); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	// A future HTTP date yields a positive, roughly-correct delay.
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(future)
	if got < 80*time.Second || got > 90*time.Second {
		t.Errorf("parseRetryAfter(future) = %v, want ~90s", got)
	}
}

func TestRateLimitWaitWithinWindow(t *testing.T) {
	// Without Retry-After the wait must always land inside [min, max].
	for i := 0; i < 2000; i++ {
		got := rateLimitWait(0)
		if got < rateLimitMinWait || got > rateLimitMaxWait {
			t.Fatalf("rateLimitWait(0) = %v, outside [%v, %v]",
				got, rateLimitMinWait, rateLimitMaxWait)
		}
	}
}

func TestRateLimitWaitIsRandom(t *testing.T) {
	// Guard against a constant/degenerate wait, which would defeat the point of
	// jitter: concurrent workers would retry in lockstep.
	seen := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		seen[rateLimitWait(0)] = true
	}
	if len(seen) < 10 {
		t.Errorf("rateLimitWait produced only %d distinct values in 50 draws; want jitter", len(seen))
	}
}

func TestRateLimitWaitHonoursRetryAfter(t *testing.T) {
	if got := rateLimitWait(12 * time.Second); got != 12*time.Second {
		t.Errorf("rateLimitWait(12s) = %v, want 12s", got)
	}
	// An absurd Retry-After is clamped so one hostile header can't stall a run.
	if got := rateLimitWait(2 * time.Hour); got != rateLimitMaxWait {
		t.Errorf("rateLimitWait(2h) = %v, want clamp to %v", got, rateLimitMaxWait)
	}
}

// testClient points a Client at a test server, with waits made negligible by a
// short Retry-After so the test doesn't actually sleep for minutes.
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := &Client{
		HTTP: srv.Client(),
		Auth: &Auth{Token: "test-token", CountryCode: "GB"},
		Log:  log.New(io.Discard, "", 0),
	}
	return c, srv
}

func TestGetRetriesOn429(t *testing.T) {
	var calls int
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			// 1s Retry-After keeps the test fast while exercising the real path.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id": 99, "title": "Recovered"}`)
	})

	var al Album
	err := c.getURL(context.Background(), srv.URL, nil, &al)
	if err != nil {
		t.Fatalf("get after 429s: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two 429s then success)", calls)
	}
	if al.Title != "Recovered" {
		t.Errorf("title = %q, want %q", al.Title, "Recovered")
	}
}

func TestGetGivesUpAfterRepeated429(t *testing.T) {
	var calls int
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	var al Album
	err := c.getURL(context.Background(), srv.URL, nil, &al)
	if err == nil {
		t.Fatal("expected an error after persistent 429s")
	}
	var rl rateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("error = %v, want a rateLimitedError", err)
	}
	if calls != rateLimitAttempts {
		t.Errorf("calls = %d, want %d", calls, rateLimitAttempts)
	}
}

func TestGetCancelsDuringBackoff(t *testing.T) {
	// A long Retry-After must not pin a run open: cancelling the context has to
	// break out of the wait promptly.
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", strconv.Itoa(int(rateLimitMaxWait.Seconds())))
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	var al Album
	err := c.getURL(ctx, srv.URL, nil, &al)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v to honour cancellation; want prompt return", elapsed)
	}
}

func TestGetDoesNotRetryOtherErrors(t *testing.T) {
	// A 404 is terminal — retrying it would just waste a run's time.
	var calls int
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})

	var al Album
	if err := c.getURL(context.Background(), srv.URL, nil, &al); err == nil {
		t.Fatal("expected an error on 404")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 404)", calls)
	}
}
