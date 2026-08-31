package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// Two episodes of one show. m00pub01 is published; m00new03 is in BBC
// metadata but its playlist is not out yet, which is the issue #52
// shape: a client that cannot tell them apart grabs the second one and
// the download fails not-yet-available.
const searchFixtureJSON = `{
  "new_search": {
    "page": 1,
    "per_page": 20,
    "results": [
      {
        "id": "m00pub01",
        "type": "episode",
        "title": "Match of the Day",
        "subtitle": "2026/27: 01. Saturday",
        "synopses": {"small": "Highlights."},
        "master_brand": {"titles": {"small": "BBC One"}},
        "release_date": "2026-08-23",
        "parent_position": 1,
        "tleo_id": "b007t9y1"
      },
      {
        "id": "m00new03",
        "type": "episode",
        "title": "Match of the Day",
        "subtitle": "2026/27: 02. Saturday",
        "synopses": {"small": "Highlights."},
        "master_brand": {"titles": {"small": "BBC One"}},
        "release_date": "2026-08-30",
        "parent_position": 2,
        "tleo_id": "b007t9y1"
      }
    ]
  }
}`

// fakeSearchProber records what it was asked to probe and returns a
// canned verdict. delay lets a test drive the probe past the endpoint's
// deadline without a real network.
type fakeSearchProber struct {
	mu    sync.Mutex
	seen  []bbc.ProbeItem
	calls int
	out   bbc.PrefetchResult
	delay time.Duration

	// echo makes the fake answer only for the PIDs it was actually
	// handed, the way a real prober does. Without it a canned `out`
	// would report a verdict for a PID that was never submitted, which
	// would hide a missing fan-out cap.
	echo bool

	// deadline records whether the context carried one, and how long
	// was left on it, so a test can assert the handler bounded the
	// probe rather than assert that the fake bounds itself.
	hadDeadline bool
	leftOnEntry time.Duration
}

func (f *fakeSearchProber) PrefetchPIDsIndividually(ctx context.Context, items []bbc.ProbeItem) bbc.PrefetchResult {
	dl, ok := ctx.Deadline()

	f.mu.Lock()
	f.calls++
	f.seen = append(f.seen, items...)
	f.hadDeadline = ok
	if ok {
		f.leftOnEntry = time.Until(dl)
	}
	delay := f.delay
	out := f.out
	echo := f.echo
	f.mu.Unlock()

	if echo {
		out = bbc.PrefetchResult{
			Heights:         make(map[string][]int, len(items)),
			NotYetAvailable: map[string]bool{},
		}
		for _, it := range items {
			out.Heights[it.PID] = []int{720}
		}
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			// The caller's deadline fired: nothing probed, every PID
			// unknown.
			return bbc.PrefetchResult{
				Heights:         map[string][]int{},
				NotYetAvailable: map[string]bool{},
			}
		}
	}
	return out
}

func (f *fakeSearchProber) probedPIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var pids []string
	for _, it := range f.seen {
		pids = append(pids, it.PID)
	}
	sort.Strings(pids)
	return pids
}

// searchRow decodes only the fields this test cares about. Decoding into
// a struct rather than the concrete response type keeps the assertion
// honest about the JSON keys a third-party client actually sees.
type searchRow struct {
	PID          string
	Title        string
	Subtitle     string
	Channel      string
	Availability string
}

func searchAPI(t *testing.T, body string) (*Handler, func()) {
	t.Helper()
	h, _ := testAPI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	h.ibl.BaseURL = srv.URL
	return h, srv.Close
}

func doSearch(t *testing.T, h *Handler) []searchRow {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/search?q=match+of+the+day&apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var rows []searchRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return rows
}

// TestSearch_MarksAvailability is the client-facing half of issue #52:
// /api/search must say which results are actually grabbable, so a script
// driving the SAB shim can skip an episode BBC has not published.
func TestSearch_MarksAvailability(t *testing.T) {
	h, closeSrv := searchAPI(t, searchFixtureJSON)
	defer closeSrv()

	prober := &fakeSearchProber{out: bbc.PrefetchResult{
		Heights:         map[string][]int{"m00pub01": {1080, 720}},
		NotYetAvailable: map[string]bool{"m00new03": true},
	}}
	h.SetProber(prober)

	rows := doSearch(t, h)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2: %+v", len(rows), rows)
	}

	byPID := map[string]searchRow{}
	for _, r := range rows {
		byPID[r.PID] = r
	}
	if got := byPID["m00pub01"].Availability; got != "available" {
		t.Errorf("published episode Availability = %q, want %q", got, "available")
	}
	if got := byPID["m00new03"].Availability; got != "not_yet_available" {
		t.Errorf("unpublished episode Availability = %q, want %q", got, "not_yet_available")
	}

	// Additive only: the existing fields the SPA Search page reads must
	// survive untouched.
	if got := byPID["m00pub01"].Title; got != "Match of the Day" {
		t.Errorf("Title = %q, want %q", got, "Match of the Day")
	}
	if got := byPID["m00pub01"].Channel; got != "BBC One" {
		t.Errorf("Channel = %q, want %q", got, "BBC One")
	}
	if byPID["m00new03"].Subtitle == "" {
		t.Error("Subtitle dropped from the search response")
	}

	// Every returned PID must be probed, each as its own item, so one
	// episode's verdict cannot be inherited from a sibling.
	want := []string{"m00new03", "m00pub01"}
	got := prober.probedPIDs()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("probed PIDs = %v, want %v", got, want)
	}
	for _, it := range prober.seen {
		if it.ShowName == "" {
			t.Errorf("ProbeItem %s has no ShowName; the quality cache row would lose it", it.PID)
		}
	}
}

// TestSearch_NilProberStillReturnsResults is the graceful-degradation
// requirement. A search that returns nothing is worse than a search that
// returns unmarked results, so with no prober wired the endpoint still
// answers and reports availability as unknown rather than guessing.
func TestSearch_NilProberStillReturnsResults(t *testing.T) {
	h, closeSrv := searchAPI(t, searchFixtureJSON)
	defer closeSrv()

	rows := doSearch(t, h)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.PID == "" || r.Title == "" {
			t.Errorf("result degraded to an empty row: %+v", r)
		}
		if r.Availability != "unknown" {
			t.Errorf("PID %s Availability = %q, want %q with no prober", r.PID, r.Availability, "unknown")
		}
	}
}

// TestSearch_HandlerBoundsTheProbeDeadline asserts the thing that
// actually protects production: the handler hands the prober a context
// that already carries a deadline of its own, sized by
// searchProbeTimeout. A real prober honours ctx, so this is what stops a
// slow BBC hanging the endpoint. Asserting on a fake that cancels itself
// would only prove the fake works.
func TestSearch_HandlerBoundsTheProbeDeadline(t *testing.T) {
	h, closeSrv := searchAPI(t, searchFixtureJSON)
	defer closeSrv()

	prober := &fakeSearchProber{echo: true}
	h.SetProber(prober)
	h.searchProbeTimeout = 250 * time.Millisecond

	doSearch(t, h)

	if !prober.hadDeadline {
		t.Fatal("prober was handed a context with no deadline; a slow BBC would hang /api/search")
	}
	// The window must be the handler's, not something looser inherited
	// from the request. Allow only for scheduling slop on entry.
	if prober.leftOnEntry <= 0 || prober.leftOnEntry > 250*time.Millisecond {
		t.Errorf("deadline left on entry = %v, want (0, 250ms]", prober.leftOnEntry)
	}
}

// TestSearch_ProbeTimeoutStillReturnsResults: a probe that answers for
// nothing must still yield a full result set, marked unknown. The
// prober here blocks until the handler's own deadline fires, so the
// wall-clock bound is tight enough to fail if the handler ever stopped
// bounding the call: the fake would otherwise block for a full minute.
func TestSearch_ProbeTimeoutStillReturnsResults(t *testing.T) {
	h, closeSrv := searchAPI(t, searchFixtureJSON)
	defer closeSrv()

	h.SetProber(&fakeSearchProber{delay: time.Minute})
	h.searchProbeTimeout = 20 * time.Millisecond

	start := time.Now()
	rows := doSearch(t, h)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("search took %v against a 20ms probe deadline; the handler is not bounding the probe", elapsed)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Availability != "unknown" {
			t.Errorf("PID %s Availability = %q, want %q after a probe timeout", r.PID, r.Availability, "unknown")
		}
	}
}

// TestSearch_ProbeFanOutIsCapped: /api/search is reached from a
// debounced keystroke on the SPA Search page, and a brand expansion can
// return far more results than a single search page. The probe fan-out
// is capped so one keystroke cannot fire hundreds of BBC round trips.
// Results past the cap must still be returned, marked unknown, never
// dropped from the response.
func TestSearch_ProbeFanOutIsCapped(t *testing.T) {
	h, _ := testAPI(t)
	prober := &fakeSearchProber{echo: true}
	h.SetProber(prober)

	total := maxSearchProbeResults + 5
	results := make([]bbc.IBLResult, 0, total)
	for i := 0; i < total; i++ {
		results = append(results, bbc.IBLResult{
			PID:   fmt.Sprintf("m00cap%03d", i),
			Title: "Capped Show",
		})
	}

	out := h.markAvailability(context.Background(), results)

	if len(out) != total {
		t.Fatalf("len(out) = %d, want %d: results past the cap must not be dropped", len(out), total)
	}
	if got := len(prober.seen); got != maxSearchProbeResults {
		t.Errorf("probed %d PIDs, want the cap of %d", got, maxSearchProbeResults)
	}
	for i, r := range out {
		want := availabilityAvailable
		if i >= maxSearchProbeResults {
			want = availabilityUnknown
		}
		if r.Availability != want {
			t.Errorf("result %d (%s) Availability = %q, want %q", i, r.PID, r.Availability, want)
		}
	}
}

// TestSearch_EmptyQueryUnchanged pins the existing envelope: no query
// still yields a bare JSON array and never touches the prober.
func TestSearch_EmptyQueryUnchanged(t *testing.T) {
	h, closeSrv := searchAPI(t, searchFixtureJSON)
	defer closeSrv()

	prober := &fakeSearchProber{}
	h.SetProber(prober)

	req := httptest.NewRequest("GET", "/api/search?q=&apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "[]\n" && got != "[]" {
		t.Errorf("empty-query body = %q, want an empty JSON array", got)
	}
	if prober.calls != 0 {
		t.Errorf("prober called %d times for an empty query, want 0", prober.calls)
	}
}
