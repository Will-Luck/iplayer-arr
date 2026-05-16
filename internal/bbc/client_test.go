package bbc

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientRetries5xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient()
	body, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestClientBailsOn4xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient()
	_, err := c.Get(srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("attempts = %d, want 1 (should not retry 4xx)", attempts)
	}
}

func TestClientSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient()
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotUA == "" || gotUA == "Go-http-client/1.1" {
		t.Errorf("UA should be a browser UA, got %q", gotUA)
	}
}

// TestClientRotatesUserAgent verifies that the UA picked for each
// request is independent: a single Client should not pin a UA for its
// whole lifetime. The desktopUserAgents pool has 9 entries, so 50
// random picks are extremely likely to surface at least 2 distinct
// values. Audit item 36.
func TestClientRotatesUserAgent(t *testing.T) {
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Header.Get("User-Agent")] = true
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient()
	for i := 0; i < 50; i++ {
		if _, err := c.Get(srv.URL); err != nil {
			t.Fatalf("Get attempt %d: %v", i, err)
		}
	}
	if len(seen) < 2 {
		t.Errorf("only %d unique UA seen across 50 requests; rotation appears stuck", len(seen))
	}
}
