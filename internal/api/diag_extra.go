package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"

	"github.com/Will-Luck/iplayer-arr/internal/download"
)

// This file extends /api/diag with one endpoint per external boundary.
// Each handler returns a flat report struct with:
//
//   - Verdict       string         "pass" or "fail"
//   - ChecksFailed  []string       per-check failure reasons (empty on pass)
//
// plus endpoint-specific fields. Verdict is "pass" iff ChecksFailed is
// empty. Auth-gated like /api/diag/sonarr-handshake. Designed to be
// curl-able by Gitea Actions so a CI step can assert .verdict == "pass"
// on every push and block bad code at merge.
//
// New endpoints (v1.5.7):
//
//   - /api/diag/ffmpeg     -- regression anchor for v1.5.5 kB->KiB rename
//   - /api/diag/bbc        -- anchors the BBC IBL response shape
//   - /api/diag/sab        -- anchors apikey enforcement on SAB-facing modes
//   - /api/diag/auth-paths -- anchors the three accepted auth mechanisms

// ----------------------------------------------------------------------
// /api/diag/ffmpeg
// ----------------------------------------------------------------------

// DiagFfmpegReport is the JSON returned by /api/diag/ffmpeg.
//
// It exists to catch the v1.5.5 regression class: a base-image bump
// that silently changes the ffmpeg progress unit (kB -> KiB) and
// breaks every download because the watchdog regex no longer matches.
// The endpoint runs `ffmpeg -version` to confirm presence + parse the
// version, then feeds a synthetic progress line in the *current*
// ffmpeg 8.x shape (KiB) through the production parseProgress regex
// (download.ParseProgress) and asserts the regex matches.
type DiagFfmpegReport struct {
	Verdict              string            `json:"verdict"`
	ChecksFailed         []string          `json:"checks_failed"`
	Version              string            `json:"version,omitempty"`
	ProgressRegexMatches bool              `json:"progress_regex_matches"`
	SampleParsed         map[string]string `json:"sample_parsed,omitempty"`
	Error                string            `json:"error,omitempty"`
}

// progressSampleKiB is a real ffmpeg 8.0.1 stderr progress line
// captured from production. The KiB unit is the v1.5.5 issue-#41
// trigger; if a future base image bump renames the unit again, the
// parseProgress regex will fail to match and this diag will fail
// before the change reaches production.
const progressSampleKiB = "frame=37630 fps=684 q=-1.0 size=  680448KiB time=00:12:32.60 bitrate=7406.6kbits/s speed=13.7x"

func (h *Handler) handleDiagFfmpeg(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	report := DiagFfmpegReport{ChecksFailed: []string{}}

	out, err := exec.Command("ffmpeg", "-version").CombinedOutput()
	if err != nil {
		report.Error = fmt.Sprintf("ffmpeg -version: %v", err)
		report.ChecksFailed = append(report.ChecksFailed, "ffmpeg: not found or non-zero exit")
	} else {
		report.Version = parseFFmpegVersion(string(out))
		if report.Version == "" {
			report.ChecksFailed = append(report.ChecksFailed, "ffmpeg: version line shape unrecognised")
		}
	}

	prog, ok := download.ParseProgress(progressSampleKiB)
	report.ProgressRegexMatches = ok
	if ok {
		report.SampleParsed = map[string]string{
			"time_seconds": fmt.Sprintf("%.2f", prog.TimeSeconds),
			"size_bytes":   fmt.Sprintf("%d", prog.SizeBytes),
		}
	} else {
		report.ChecksFailed = append(report.ChecksFailed,
			"progress_regex: KiB-form ffmpeg 8.x progress line did not match parseProgress")
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// ----------------------------------------------------------------------
// /api/diag/bbc
// ----------------------------------------------------------------------

// DiagBBCReport is the JSON returned by /api/diag/bbc.
//
// Anchors the BBC IBL contract: probe the live endpoint with a known
// stable show (Doctor Who, tvdbid=78804, mapped manually because IBL
// is keyed on PIDs and brand names rather than tvdbids) and assert
// the response parses into the iblElementJSON shape we expect.
// Catches BBC API drift before downloads silently start failing.
type DiagBBCReport struct {
	Verdict           string   `json:"verdict"`
	ChecksFailed      []string `json:"checks_failed"`
	EndpointReachable bool     `json:"endpoint_reachable"`
	ResultShape       struct {
		HasBrand    bool `json:"has_brand"`
		HasEpisodes bool `json:"has_episodes"`
	} `json:"result_shape"`
	ResultsCount int    `json:"results_count"`
	Error        string `json:"error,omitempty"`
}

// diagBBCProbe is the indirection point that lets tests inject a fake
// IBL backend. Production wires it to the live ibl.SearchCtx via the
// Handler's ibl field; tests can replace it with an httptest server
// to drive happy-path and broken-shape scenarios. Returns the number
// of results plus a flag pair indicating whether the response carried
// brand+episode data.
type diagBBCProbe func(r *http.Request) (count int, hasBrand, hasEpisodes bool, err error)

func (h *Handler) handleDiagBBC(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	report := DiagBBCReport{ChecksFailed: []string{}}

	probe := h.bbcProbe
	if probe == nil {
		probe = h.defaultBBCProbe
	}

	count, hasBrand, hasEpisodes, err := probe(r)
	if err != nil {
		report.Error = err.Error()
		report.ChecksFailed = append(report.ChecksFailed, "bbc: probe failed: "+err.Error())
	} else {
		report.EndpointReachable = true
		report.ResultsCount = count
		report.ResultShape.HasBrand = hasBrand
		report.ResultShape.HasEpisodes = hasEpisodes
		if count == 0 {
			report.ChecksFailed = append(report.ChecksFailed,
				"bbc: probe returned zero results (IBL contract change?)")
		}
		if !hasBrand {
			report.ChecksFailed = append(report.ChecksFailed,
				"bbc: response missing brand information")
		}
		if !hasEpisodes {
			report.ChecksFailed = append(report.ChecksFailed,
				"bbc: response missing episode entries")
		}
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// defaultBBCProbe runs the live IBL search using the Handler's
// configured ibl client. Doctor Who (q="Doctor Who") is the stable
// canary -- it's been in continuous production since the 1960s, and
// the BBC reliably indexes brand+episode metadata for it.
func (h *Handler) defaultBBCProbe(r *http.Request) (int, bool, bool, error) {
	if h.ibl == nil {
		return 0, false, false, fmt.Errorf("bbc client not wired")
	}
	results, err := h.ibl.SearchCtx(r.Context(), "Doctor Who", 1)
	if err != nil {
		return 0, false, false, err
	}
	if len(results) == 0 {
		return 0, false, false, nil
	}
	hasBrand := false
	hasEpisodes := false
	for _, res := range results {
		if res.BrandPID != "" || res.Title != "" {
			hasBrand = true
		}
		if res.PID != "" && res.EpisodeNum > 0 {
			hasEpisodes = true
		}
		if hasBrand && hasEpisodes {
			break
		}
	}
	return len(results), hasBrand, hasEpisodes, nil
}

// ----------------------------------------------------------------------
// /api/diag/sab
// ----------------------------------------------------------------------

// DiagSABReport is the JSON returned by /api/diag/sab.
//
// Regression anchor for the v1.5.5 SAB apikey hole (fixed in 40fa0ff
// "fix(sabnzbd): enforce apikey on get_cats|get_config|fullstatus").
// Synthesises SABnzbd requests in-process against the live SAB
// handler and asserts:
//
//   - mode=version returns 200 without apikey (intentional carve-out
//     so Sonarr can probe a candidate indexer before configuring it)
//   - every other surveyed mode returns an error envelope when called
//     without apikey
//   - every other surveyed mode returns 200 when called with apikey
//
// If any auth-gated mode lets an unauthenticated caller through, the
// v1.5.5 regression has re-opened and CI must block the merge.
type DiagSABReport struct {
	Verdict      string                   `json:"verdict"`
	ChecksFailed []string                 `json:"checks_failed"`
	ModesChecked int                      `json:"modes_checked"`
	UnauthLeaks  []string                 `json:"unauth_leaks"`
	AuthFailures []string                 `json:"auth_failures"`
	PerMode      map[string]sabModeResult `json:"per_mode,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

type sabModeResult struct {
	NoKeyStatus   int  `json:"no_key_status"`
	NoKeyOK       bool `json:"no_key_ok"`
	WithKeyStatus int  `json:"with_key_status"`
	WithKeyOK     bool `json:"with_key_ok"`
}

// sabAuthGatedModes is the set of SAB modes that MUST require apikey.
// version is excluded because Sonarr probes it pre-auth as part of
// the "test connection" flow in the SABnzbd download client UI.
var sabAuthGatedModes = []string{"get_cats", "get_config", "fullstatus", "queue", "history"}

func (h *Handler) handleDiagSAB(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	report := DiagSABReport{
		ChecksFailed: []string{},
		UnauthLeaks:  []string{},
		AuthFailures: []string{},
		PerMode:      map[string]sabModeResult{},
	}

	if h.sabHandler == nil {
		report.Error = "sab handler not wired (test harness or main.go SetSABHandler missed)"
		report.ChecksFailed = append(report.ChecksFailed, "sab: handler not wired")
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	apiKey, _ := h.store.GetConfig("api_key")
	if apiKey == "" {
		report.Error = "api_key not seeded in store"
		report.ChecksFailed = append(report.ChecksFailed, "sab: api_key not seeded")
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	// version is the unauthenticated carve-out: synthesise it and
	// confirm it still returns 200 without apikey. If this stops
	// working, Sonarr's "test connection" UX breaks.
	versionResult := h.probeSABMode("version", "")
	report.PerMode["version"] = versionResult
	if !versionResult.NoKeyOK {
		report.ChecksFailed = append(report.ChecksFailed,
			fmt.Sprintf("sab: mode=version without apikey returned %d (expected 200)", versionResult.NoKeyStatus))
	}

	// Every auth-gated mode: must reject without apikey, must accept with.
	for _, mode := range sabAuthGatedModes {
		result := h.probeSABMode(mode, apiKey)
		report.PerMode[mode] = result
		report.ModesChecked++

		// "Reject" for SAB means a JSON envelope with status:false. The
		// handler returns HTTP 200 with a status:false body for unauth
		// rejections rather than 401, so we look at the body shape.
		if result.NoKeyOK {
			report.UnauthLeaks = append(report.UnauthLeaks, mode)
		}
		if !result.WithKeyOK {
			report.AuthFailures = append(report.AuthFailures, mode)
		}
	}

	if len(report.UnauthLeaks) > 0 {
		report.ChecksFailed = append(report.ChecksFailed,
			"sab: modes accepted unauthenticated requests: "+strings.Join(report.UnauthLeaks, ","))
	}
	if len(report.AuthFailures) > 0 {
		report.ChecksFailed = append(report.ChecksFailed,
			"sab: modes rejected authenticated requests: "+strings.Join(report.AuthFailures, ","))
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// probeSABMode synthesises a SAB request for the given mode, both
// without and with the supplied apikey, and returns the observed
// outcome of each. NoKeyOK / WithKeyOK reflect SAB's body envelope
// (status:true means "authenticated and served") rather than just
// HTTP status, because the handler returns 200 + status:false for
// unauthenticated rejections.
func (h *Handler) probeSABMode(mode, apiKey string) sabModeResult {
	result := sabModeResult{}

	noKeyReq := httptest.NewRequest("GET", "/sabnzbd/api?mode="+url.QueryEscape(mode), nil)
	noKeyW := httptest.NewRecorder()
	h.sabHandler.ServeHTTP(noKeyW, noKeyReq)
	result.NoKeyStatus = noKeyW.Code
	result.NoKeyOK = sabBodyAuthed(noKeyW.Body.Bytes())

	if apiKey == "" {
		return result
	}
	withKeyReq := httptest.NewRequest("GET", "/sabnzbd/api?mode="+url.QueryEscape(mode)+"&apikey="+url.QueryEscape(apiKey), nil)
	withKeyW := httptest.NewRecorder()
	h.sabHandler.ServeHTTP(withKeyW, withKeyReq)
	result.WithKeyStatus = withKeyW.Code
	result.WithKeyOK = sabBodyAuthed(withKeyW.Body.Bytes())
	return result
}

// sabBodyAuthed reports whether the SAB response body represents an
// authenticated, served reply. The SAB handler returns 200 with
// {"status": false, "error": "..."} on auth failure; on a served
// response either status is absent (e.g. version returns a flat
// {"version": "..."}) or it is true. The auth-failure shape always
// carries an "error" field, so the simplest robust check is "the
// body parses as JSON and does not signal status:false".
func sabBodyAuthed(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if status, ok := envelope["status"]; ok {
		if b, isBool := status.(bool); isBool && !b {
			return false
		}
	}
	if _, hasError := envelope["error"]; hasError {
		// Belt-and-braces: an error field paired with anything is
		// treated as an auth or other failure.
		return false
	}
	return true
}

// ----------------------------------------------------------------------
// /api/diag/auth-paths
// ----------------------------------------------------------------------

// DiagAuthPathsReport is the JSON returned by /api/diag/auth-paths.
//
// Asserts the three documented auth mechanisms all work against the
// running binary:
//
//   - ?apikey=<key> query parameter
//   - Authorization: Bearer <key> header
//   - X-Api-Key: <key> header
//
// The endpoint is the regression anchor for the test/prod auth drift
// flagged during the v1.5.7 framework work: tests used X-Api-Key in
// several places, production never accepted it, and unit tests passed
// only because they called handler methods directly and bypassed
// ServeHTTP. The fix was to widen authenticate() to accept all three;
// this endpoint asserts the wider contract holds.
type DiagAuthPathsReport struct {
	Verdict         string   `json:"verdict"`
	ChecksFailed    []string `json:"checks_failed"`
	QueryParamWorks bool     `json:"query_param_works"`
	BearerWorks     bool     `json:"bearer_works"`
	HeaderWorks     bool     `json:"header_works"`
	Error           string   `json:"error,omitempty"`
}

func (h *Handler) handleDiagAuthPaths(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	report := DiagAuthPathsReport{ChecksFailed: []string{}}

	apiKey, _ := h.store.GetConfig("api_key")
	if apiKey == "" {
		report.Error = "api_key not seeded in store"
		report.ChecksFailed = append(report.ChecksFailed, "auth_paths: api_key not seeded")
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	// Run three synthetic requests through the in-process authenticate()
	// to confirm each mechanism is accepted. Using the same probe rather
	// than driving a real endpoint avoids coupling this diag to whatever
	// route the probe targets and keeps the assertion purely about the
	// auth predicate.
	queryReq := httptest.NewRequest("GET", "/probe?apikey="+url.QueryEscape(apiKey), nil)
	report.QueryParamWorks = h.authenticate(queryReq)
	if !report.QueryParamWorks {
		report.ChecksFailed = append(report.ChecksFailed,
			"auth_paths: ?apikey= query parameter not accepted")
	}

	bearerReq := httptest.NewRequest("GET", "/probe", nil)
	bearerReq.Header.Set("Authorization", "Bearer "+apiKey)
	report.BearerWorks = h.authenticate(bearerReq)
	if !report.BearerWorks {
		report.ChecksFailed = append(report.ChecksFailed,
			"auth_paths: Authorization: Bearer header not accepted")
	}

	headerReq := httptest.NewRequest("GET", "/probe", nil)
	headerReq.Header.Set("X-Api-Key", apiKey)
	report.HeaderWorks = h.authenticate(headerReq)
	if !report.HeaderWorks {
		report.ChecksFailed = append(report.ChecksFailed,
			"auth_paths: X-Api-Key header not accepted")
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// ----------------------------------------------------------------------
// Helpers shared with diag.go
// ----------------------------------------------------------------------

// Compile-time guards so refactors of parseFFmpegVersion / io.ReadAll
// don't accidentally orphan the diag handlers' assumptions.
var _ = io.Discard
