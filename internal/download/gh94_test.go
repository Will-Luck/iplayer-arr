package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// gh94Manager wires a manager to an iBL server that resolves m0033jgh (the
// GitHub #57 capture, version m0033jgj) and to a mediaselector driven by
// msHandler. ffmpeg must never run: every case here fails before a stream
// is picked.
func gh94Manager(t *testing.T, msHandler http.HandlerFunc) (*Manager, *store.Store) {
	t.Helper()
	episode := readBBCFixture(t, "episode_m0033jgh.json")
	iblSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(episode)
	}))
	t.Cleanup(iblSrv.Close)
	msSrv := httptest.NewServer(msHandler)
	t.Cleanup(msSrv.Close)

	m, st := newGH57Manager(t, iblSrv.URL, msSrv.URL)
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		t.Error("ffmpeg must not run when mediaselector listed no usable stream")
		return nil
	}
	return m, st
}

// gh94Outages are the two outage shapes from the issue: a 5xx on every
// attempt, and a 302 to the BBC's HTML error page.
func gh94Outages(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()
	page := readBBCFixture(t, "bbc_error_page.html")
	return map[string]http.HandlerFunc{
		"503 on every attempt": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
		"redirect to the HTML error page": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/error.html" {
				http.Redirect(w, r, "/error.html", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write(page)
		},
	}
}

// Gitea #94: a mediaselector outage is the BBC failing to answer, not an
// answer of "no streams", so it must land as a retryable
// metadata_unavailable failure on its own budget, never as
// stream_unavailable on the shared 3-attempt budget.
func TestProcessDownload_GH94_MediaSelectorOutageIsMetadataUnavailable(t *testing.T) {
	for name, handler := range gh94Outages(t) {
		t.Run(name, func(t *testing.T) {
			m, st := gh94Manager(t, handler)
			dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
			m.processDownload(context.Background(), dl)

			if dl.FailureCode != store.FailCodeMetadataUnavailable {
				t.Fatalf("FailureCode = %q, want %q (error %q)",
					dl.FailureCode, store.FailCodeMetadataUnavailable, dl.Error)
			}
			if !dl.Retryable || dl.MetadataFailures != 1 || dl.RetryCount != 0 {
				t.Errorf("Retryable = %v, MetadataFailures = %d, RetryCount = %d; want true, 1, 0",
					dl.Retryable, dl.MetadataFailures, dl.RetryCount)
			}
			if !strings.HasPrefix(dl.Error, "media selector: ") {
				t.Errorf("Error = %q, want it to start with %q", dl.Error, "media selector: ")
			}
		})
	}
}

// The #52/#57 trap end to end: an outage that outlasts the whole metadata
// budget goes to history as metadata_unavailable, and the next grab of
// the same pid+quality starts a fresh download instead of getting the
// dead row back (Enqueue supersedes metadata_unavailable).
func TestEnqueue_GH94_ExhaustedMediaSelectorOutageIsSuperseded(t *testing.T) {
	m, st := gh94Manager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
	dl.MetadataFailures = maxMetadataFailures - 1 // this attempt is the last of the budget
	m.processDownload(context.Background(), dl)

	if dl.Retryable {
		t.Fatalf("Retryable = true after the last metadata attempt (FailureCode %q, RetryCount %d)",
			dl.FailureCode, dl.RetryCount)
	}
	hist, err := st.GetHistory(dl.ID)
	if err != nil || hist == nil {
		t.Fatalf("GetHistory(%q) = %v, %v; the exhausted row must be in history", dl.ID, hist, err)
	}
	if hist.FailureCode != store.FailCodeMetadataUnavailable {
		t.Fatalf("history FailureCode = %q, want %q", hist.FailureCode, store.FailCodeMetadataUnavailable)
	}

	id, err := m.Enqueue(dl.PID, dl.Quality, dl.Title, "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == dl.ID {
		t.Fatal("a fresh grab got the dead outage row back; it must be superseded")
	}
}

// The other side of Gitea #94: a mediaselector document with no usable
// video is the BBC's answer about the programme, so it keeps failing as
// stream_unavailable on the shared budget. Passes on the base too; it
// stops the fix from sending real answers to the hour-long retry.
func TestProcessDownload_GH94_AnswerWithoutVideoStaysStreamUnavailable(t *testing.T) {
	answers := map[string]string{
		"selectionunavailable": `<?xml version="1.0" encoding="UTF-8"?><mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection"><error id="selectionunavailable"/></mediaSelection>`,
		"captions only":        `<?xml version="1.0" encoding="UTF-8"?><mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection"><media kind="captions" type="application/ttaf+xml"><connection supplier="mf_akamai" transferFormat="plain" protocol="https" href="https://subs.example/captions.xml"/></media></mediaSelection>`,
	}
	for name, body := range answers {
		t.Run(name, func(t *testing.T) {
			m, st := gh94Manager(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
				w.Write([]byte(body))
			})
			dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
			m.processDownload(context.Background(), dl)

			if dl.FailureCode != store.FailCodeUnavailable {
				t.Fatalf("FailureCode = %q, want %q (error %q)", dl.FailureCode, store.FailCodeUnavailable, dl.Error)
			}
			if dl.MetadataFailures != 0 || dl.RetryCount != 1 {
				t.Errorf("MetadataFailures = %d, RetryCount = %d; want 0, 1", dl.MetadataFailures, dl.RetryCount)
			}
		})
	}
}
