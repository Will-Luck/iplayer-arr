package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const failoverTTML = `<?xml version="1.0" encoding="utf-8"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:05.000" end="00:00:08.000">Hello, world!</p>
    </div>
  </body>
</tt>`

// captionsSuppliers serves three captions suppliers: one that refuses
// (403), one that answers 200 with an HTML error page, and one that
// serves real TTML. hits returns the paths asked for, in order.
func captionsSuppliers(t *testing.T) (srv *httptest.Server, hits func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/refused.xml":
			w.WriteHeader(http.StatusForbidden)
		case "/errorpage.xml":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!DOCTYPE html><html lang="en-GB"><head><title>BBC - Error</title></head><body><p>Sorry</p></body></html>`))
		case "/captions.xml":
			w.Header().Set("Content-Type", "application/ttaf+xml")
			w.Write([]byte(failoverTTML))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// Gitea #91: a refusing captions supplier, or one that answers with an
// error page, must not cost the subtitles when another supplier has them.
func TestDownloadSubtitles_FailsOverToNextSupplier(t *testing.T) {
	srv, hits := captionsSuppliers(t)
	m, _ := newGH57Manager(t, srv.URL, srv.URL)
	outDir := t.TempDir()

	m.downloadSubtitles(context.Background(), []string{
		srv.URL + "/refused.xml",
		srv.URL + "/errorpage.xml",
		srv.URL + "/captions.xml",
	}, outDir, "Show.S01E01")

	srt, err := os.ReadFile(filepath.Join(outDir, "Show.S01E01.en.srt"))
	if err != nil {
		t.Fatalf("no subtitles written after two suppliers failed: %v (asked for %v)", err, hits())
	}
	if !strings.Contains(string(srt), "Hello, world!") {
		t.Errorf("srt = %q, want the third supplier's captions", srt)
	}
	if got, want := strings.Join(hits(), " "), "/refused.xml /errorpage.xml /captions.xml"; got != want {
		t.Errorf("suppliers asked = %q, want %q", got, want)
	}
}

// A working first supplier is the only one asked.
func TestDownloadSubtitles_StopsAtFirstWorkingSupplier(t *testing.T) {
	srv, hits := captionsSuppliers(t)
	m, _ := newGH57Manager(t, srv.URL, srv.URL)
	outDir := t.TempDir()

	m.downloadSubtitles(context.Background(), []string{srv.URL + "/captions.xml", srv.URL + "/refused.xml"}, outDir, "Show.S01E01")

	if _, err := os.Stat(filepath.Join(outDir, "Show.S01E01.en.srt")); err != nil {
		t.Fatalf("subtitles not written: %v", err)
	}
	if got := hits(); len(got) != 1 || got[0] != "/captions.xml" {
		t.Errorf("suppliers asked = %v, want only /captions.xml", got)
	}
}

// A cancelled worker context (shutdown, user cancel) must stop the
// captions fetch before any supplier is asked.
func TestDownloadSubtitles_CancelledContextAsksNoSupplier(t *testing.T) {
	srv, hits := captionsSuppliers(t)
	m, _ := newGH57Manager(t, srv.URL, srv.URL)
	outDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m.downloadSubtitles(ctx, []string{srv.URL + "/captions.xml"}, outDir, "Show.S01E01")

	if got := hits(); len(got) != 0 {
		t.Errorf("suppliers asked = %v, want none after cancel", got)
	}
}
