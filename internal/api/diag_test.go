package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeNewznabHandler simulates the real newznab.Handler enough for
// the diag endpoint to exercise its parsing and round-trip logic.
// Two configurable axes: whether tvsearch returns apikey-embedded
// URLs (the v1.5.5 regression shape), and whether the grab
// roundtrip returns a 200 NZB or a 401 envelope.
type fakeNewznabHandler struct {
	rssBody    string
	grabStatus int
	grabType   string
	grabBody   string
}

func (f *fakeNewznabHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	switch t {
	case "tvsearch":
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(f.rssBody))
	case "get":
		ct := f.grabType
		if ct == "" {
			ct = "application/x-nzb"
		}
		w.Header().Set("Content-Type", ct)
		status := f.grabStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		w.Write([]byte(f.grabBody))
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// testDiagAPI builds an api.Handler wired with a fake store seeded
// with the standard test-api-key plus a fakeNewznabHandler. Returns
// the handler and the fake so individual tests can tweak the fake's
// behaviour mid-test.
func testDiagAPI(t *testing.T, rssBody, grabBody string, grabStatus int, grabType string) (*Handler, *fakeNewznabHandler) {
	h, _ := testAPI(t)
	fake := &fakeNewznabHandler{
		rssBody:    rssBody,
		grabBody:   grabBody,
		grabStatus: grabStatus,
		grabType:   grabType,
	}
	h.SetNewznabHandler(fake)
	return h, fake
}

const validFeedWithAPIKey = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>iplayer-arr</title>
    <item>
      <guid isPermaLink="true">http://host/newznab/api?t=get&amp;id=abc&amp;apikey=test-api-key</guid>
      <link>http://host/newznab/api?t=get&amp;id=abc&amp;apikey=test-api-key</link>
      <enclosure url="http://host/newznab/api?t=get&amp;id=abc&amp;apikey=test-api-key" length="100" type="application/x-nzb" />
    </item>
    <item>
      <guid isPermaLink="true">http://host/newznab/api?t=get&amp;id=def&amp;apikey=test-api-key</guid>
      <link>http://host/newznab/api?t=get&amp;id=def&amp;apikey=test-api-key</link>
      <enclosure url="http://host/newznab/api?t=get&amp;id=def&amp;apikey=test-api-key" length="200" type="application/x-nzb" />
    </item>
  </channel>
</rss>`

// regressionShapeFeedNoAPIKey reproduces the v1.5.5 issue #40 bug:
// the security commit enforced apikey on `t=get` but the companion
// feed-embed fix hadn't landed yet, so URLs went out without apikey.
// The diag endpoint must detect this and emit verdict=fail.
const regressionShapeFeedNoAPIKey = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>iplayer-arr</title>
    <item>
      <guid isPermaLink="true">http://host/newznab/api?t=get&amp;id=abc</guid>
      <link>http://host/newznab/api?t=get&amp;id=abc</link>
      <enclosure url="http://host/newznab/api?t=get&amp;id=abc" length="100" type="application/x-nzb" />
    </item>
  </channel>
</rss>`

func TestDiagSonarrHandshake_NoAuth(t *testing.T) {
	h, _ := testDiagAPI(t, validFeedWithAPIKey, "<?xml?><nzb/>", 200, "application/x-nzb")
	req := httptest.NewRequest("GET", "/api/diag/sonarr-handshake", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestDiagSonarrHandshake_HappyPath verifies that a correctly-shaped
// feed (URLs include apikey) and a 200 NZB grab both pass, yielding
// a `verdict: pass` with empty `checks_failed`.
func TestDiagSonarrHandshake_HappyPath(t *testing.T) {
	h, _ := testDiagAPI(t, validFeedWithAPIKey, "<?xml?><nzb><file/></nzb>", 200, "application/x-nzb")
	req := authedRequest("GET", "/api/diag/sonarr-handshake?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var report DiagSonarrReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}

	if !report.Newznab.OK || report.Newznab.ItemsReturned != 2 {
		t.Errorf("newznab check: %+v", report.Newznab)
	}
	if !report.FeedAPIKey.OK || report.FeedAPIKey.MissingURLs != 0 {
		t.Errorf("feed_apikey check: %+v", report.FeedAPIKey)
	}
	if !report.GrabRoundtrip.OK || report.GrabRoundtrip.HTTPCode != 200 {
		t.Errorf("grab check: %+v", report.GrabRoundtrip)
	}
	if !report.Store.OK {
		t.Errorf("store check: %+v", report.Store)
	}
	// ffmpeg + geo depend on host env; don't gate on them in unit test.
}

// TestDiagSonarrHandshake_DetectsRegression is the issue #40 unit
// test: a feed missing `apikey=` in its URLs must trip the
// feed_apikey check and surface in checks_failed. This locks in the
// guarantee that v1.5.6+ CI catches the exact regression that
// shipped to users in v1.5.5.
func TestDiagSonarrHandshake_DetectsRegression(t *testing.T) {
	// fake grab: returns 401 because Sonarr would have followed the
	// URL with no apikey and hit the auth gate. Matches the real
	// failure shape emersnbe reported on issue #40.
	h, _ := testDiagAPI(t, regressionShapeFeedNoAPIKey,
		`<?xml version="1.0" encoding="UTF-8"?><error code="100" description="Invalid API Key"/>`,
		http.StatusUnauthorized, "application/xml")
	req := authedRequest("GET", "/api/diag/sonarr-handshake?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var report DiagSonarrReport
	json.Unmarshal(w.Body.Bytes(), &report)

	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if report.FeedAPIKey.OK || report.FeedAPIKey.MissingURLs != 3 {
		t.Errorf("expected feed_apikey to detect 3 missing apikeys (guid+link+enclosure), got %+v", report.FeedAPIKey)
	}
	if report.GrabRoundtrip.OK {
		t.Errorf("expected grab to fail with 401, got %+v", report.GrabRoundtrip)
	}

	foundFeedFail := false
	foundGrabFail := false
	for _, msg := range report.ChecksFailed {
		if strings.Contains(msg, "feed_apikey") {
			foundFeedFail = true
		}
		if strings.Contains(msg, "grab_roundtrip") {
			foundGrabFail = true
		}
	}
	if !foundFeedFail || !foundGrabFail {
		t.Errorf("checks_failed missing one or both of feed_apikey/grab_roundtrip: %v", report.ChecksFailed)
	}
}

func TestDiagSonarrHandshake_NewznabNotWired(t *testing.T) {
	h, _ := testAPI(t)
	// Intentionally skip SetNewznabHandler so the diag endpoint must
	// degrade rather than NPE.
	req := authedRequest("GET", "/api/diag/sonarr-handshake?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (endpoint must always JSON-respond)", w.Code)
	}
	var report DiagSonarrReport
	json.Unmarshal(w.Body.Bytes(), &report)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail when newznab unwired", report.Verdict)
	}
	if !strings.Contains(report.Newznab.Error, "not wired") {
		t.Errorf("newznab.error = %q, want 'not wired' hint", report.Newznab.Error)
	}
}

func TestParseFFmpegVersion(t *testing.T) {
	cases := map[string]string{
		"ffmpeg version 8.0.1 Copyright (c) 2000-2025\n":    "8.0.1",
		"ffmpeg version n6.1-tessus-static Copyright (c)\n": "n6.1-tessus-static",
		"ffmpeg version 7.0\n":                              "7.0",
		"ffmpeg":                                            "",
		"different first line\n":                            "",
	}
	for input, want := range cases {
		got := parseFFmpegVersion(input)
		if got != want {
			t.Errorf("parseFFmpegVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStripSchemeHost(t *testing.T) {
	cases := map[string]string{
		"http://host:62001/newznab/api?t=get&id=abc": "/newznab/api?t=get&id=abc",
		"https://example.com/newznab/api?t=get":      "/newznab/api?t=get",
		"/newznab/api?t=get":                         "/newznab/api?t=get",
		"http://host":                                "http://host",
	}
	for input, want := range cases {
		got := stripSchemeHost(input)
		if got != want {
			t.Errorf("stripSchemeHost(%q) = %q, want %q", input, got, want)
		}
	}
}
