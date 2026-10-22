package bbc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
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

// newErrorPageServer reproduces BBC /programmes from 2026-10-21: every path
// is redirected to /error.html, which answers 200 text/html with the BBC's
// "Service Temporarily Unavailable" page. hits counts every request,
// redirects included. GitHub #57.
func newErrorPageServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	page, err := os.ReadFile("testdata/ibl/bbc_error_page.html")
	if err != nil {
		t.Fatalf("read error page fixture: %v", err)
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/error.html" {
			http.Redirect(w, r, "/error.html", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(page)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// GitHub #57: the redirected error page used to come back as a successful
// body, so the caller's json.Unmarshal reported "invalid character '<'".
func TestGetJSONCtx_RejectsHTMLAfterRedirect(t *testing.T) {
	srv, _ := newErrorPageServer(t)

	_, err := NewClient().GetJSONCtx(context.Background(), srv.URL+"/programmes/m0033jgh/playlist.json")
	if err == nil {
		t.Fatal("expected an error for an HTML page, got nil")
	}
	if !errors.Is(err, ErrNotJSON) {
		t.Errorf("errors.Is(err, ErrNotJSON) = false, err = %v", err)
	}
	var nj *NotJSONError
	if !errors.As(err, &nj) {
		t.Fatalf("errors.As(*NotJSONError) = false, err = %v", err)
	}
	if !strings.HasSuffix(nj.FinalURL, "/error.html") {
		t.Errorf("FinalURL = %q, want it to end in /error.html", nj.FinalURL)
	}
	if nj.PageTitle != "BBC: Service Temporarily Unavailable" {
		t.Errorf("PageTitle = %q", nj.PageTitle)
	}
	msg := err.Error()
	for _, want := range []string{"HTML", "/error.html", "Service Temporarily Unavailable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "invalid character") {
		t.Errorf("error %q still reads as a JSON parse failure", msg)
	}
}

func TestGetJSONCtx_RejectsHTMLBodyMislabelledAsJSON(t *testing.T) {
	page, err := os.ReadFile("testdata/ibl/bbc_error_page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(page)
	}))
	defer srv.Close()

	_, err = NewClient().GetJSONCtx(context.Background(), srv.URL)
	if !errors.Is(err, ErrNotJSON) {
		t.Fatalf("errors.Is(err, ErrNotJSON) = false, err = %v", err)
	}
}

func TestGetJSONCtx_RejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
	}))
	defer srv.Close()

	_, err := NewClient().GetJSONCtx(context.Background(), srv.URL)
	if !errors.Is(err, ErrNotJSON) {
		t.Fatalf("errors.Is(err, ErrNotJSON) = false, err = %v", err)
	}
}

// The BBC edge caches the error page (max-age=60), so retrying at once
// only fetches the same page again.
func TestGetJSONCtx_DoesNotRetryHTML(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!DOCTYPE html><html><head><title>x</title></head></html>"))
	}))
	defer srv.Close()

	if _, err := NewClient().GetJSONCtx(context.Background(), srv.URL); err == nil {
		t.Fatal("expected an error, got nil")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("hits = %d, want 1", n)
	}
}

// Test servers that write JSON without a Content-Type header get
// text/plain from Go's sniffer; those bodies must still pass.
func TestGetJSONCtx_AcceptsJSONWithoutContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"a":1}`))
	}))
	defer srv.Close()

	body, err := NewClient().GetJSONCtx(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetJSONCtx: %v", err)
	}
	if string(body) != `{"a":1}` {
		t.Errorf("body = %q", body)
	}
}

// The BOM is stripped from the returned body: encoding/json rejects one.
func TestGetJSONCtx_AcceptsBOMPrefixedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("\xef\xbb\xbf  {\"a\":1}"))
	}))
	defer srv.Close()

	body, err := NewClient().GetJSONCtx(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetJSONCtx: %v", err)
	}
	var v struct{ A int }
	if err := json.Unmarshal(body, &v); err != nil || v.A != 1 {
		t.Errorf("json.Unmarshal(body) = %v, a = %d; want a usable body", err, v.A)
	}
}

func TestGetJSONCtx_RejectsHTMLThatDoesNotLookLikeHTML(t *testing.T) {
	cases := map[string]struct{ contentType, body string }{
		"BOM then doctype, labelled JSON": {"application/json", "\xef\xbb\xbf<!DOCTYPE html><html></html>"},
		"text/html plain sentence":        {"text/html; charset=utf-8", "Service unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			if _, err := NewClient().GetJSONCtx(context.Background(), srv.URL); !errors.Is(err, ErrNotJSON) {
				t.Errorf("errors.Is(err, ErrNotJSON) = false, err = %v", err)
			}
		})
	}
}

// A long title is cut on a character boundary, so the error text stays
// valid UTF-8. The leading "a" puts byte 120 inside an "é", so a cut by
// bytes would split it.
func TestGetJSONCtx_LongTitleStaysValidUTF8(t *testing.T) {
	title := "a" + strings.Repeat("é", 130)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>" + title + "</title></head></html>"))
	}))
	defer srv.Close()

	_, err := NewClient().GetJSONCtx(context.Background(), srv.URL)
	var nj *NotJSONError
	if !errors.As(err, &nj) {
		t.Fatalf("errors.As(*NotJSONError) = false, err = %v", err)
	}
	if !utf8.ValidString(nj.PageTitle) || utf8.RuneCountInString(nj.PageTitle) != 120 {
		t.Errorf("PageTitle valid = %v, runes = %d; want valid UTF-8, 120 runes",
			utf8.ValidString(nj.PageTitle), utf8.RuneCountInString(nj.PageTitle))
	}
}
