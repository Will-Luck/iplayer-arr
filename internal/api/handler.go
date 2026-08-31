package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/auth"
	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/download"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// RuntimeStatus holds startup health check results for the status endpoint.
type RuntimeStatus struct {
	mu            sync.RWMutex
	FFmpegVersion string
	GeoOK         bool
	GeoStatus     string
	GeoDetail     string
	GeoCheckedAt  string
}

// StatusSnapshot is a consistent read of RuntimeStatus.
type StatusSnapshot struct {
	FFmpegVersion string
	GeoOK         bool
	GeoStatus     string
	GeoDetail     string
	GeoCheckedAt  string
}

// SetGeo updates the geo fields under the write lock. GeoOK is derived from
// the classified status so callers and the cache stay consistent.
func (rs *RuntimeStatus) SetGeo(status, detail, checkedAt string) {
	rs.mu.Lock()
	rs.GeoStatus = status
	rs.GeoDetail = detail
	rs.GeoOK = status == string(bbc.GeoUKOK)
	rs.GeoCheckedAt = checkedAt
	rs.mu.Unlock()
}

// Snapshot returns a consistent read of all RuntimeStatus fields.
func (rs *RuntimeStatus) Snapshot() StatusSnapshot {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return StatusSnapshot{
		FFmpegVersion: rs.FFmpegVersion,
		GeoOK:         rs.GeoOK,
		GeoStatus:     rs.GeoStatus,
		GeoDetail:     rs.GeoDetail,
		GeoCheckedAt:  rs.GeoCheckedAt,
	}
}

// Handler is the REST API router for the frontend dashboard.
type Handler struct {
	store  *store.Store
	hub    *Hub
	mgr    *download.Manager
	ibl    *bbc.IBL
	status *RuntimeStatus

	// lastIndexerRequest stores the most recent time Sonarr (or any Newznab
	// client) queried the indexer endpoint.  Stored as atomic.Value holding a
	// time.Time so it can be updated from the newznab goroutine without locks.
	lastIndexerRequest atomic.Value

	// Fields set after construction (exported so main.go can populate them).
	RingBuf     *RingBuffer
	StartedAt   time.Time
	DownloadDir string
	// GeoProbe, when non-nil, re-runs the BBC geo check and returns the
	// classified result (UK access, geo-block, DNS failure or connectivity
	// failure).
	GeoProbe func() bbc.GeoResult

	// newznabHandler is the live newznab.Handler so the diag endpoint
	// (handleDiagSonarrHandshake) can simulate Sonarr's tvsearch+grab
	// round-trip in-process via httptest.NewRecorder. Set via
	// SetNewznabHandler from main.go after both handlers exist (the
	// newznab handler depends on prober which depends on bbcClient,
	// so it's constructed after the api handler).
	newznabHandler http.Handler

	// sabHandler is the live sabnzbd.Handler used by /api/diag/sab to
	// synthesise SABnzbd requests in-process. Wired via SetSABHandler
	// from main.go.
	sabHandler http.Handler

	// prober, when non-nil, lets handleSearch report per-result
	// availability so an API-driven client can skip an episode BBC has
	// not published yet (issue #52). Optional: with no prober every
	// result is reported as unknown and the search still answers.
	// Wired via SetProber from main.go for the same ordering reason as
	// newznabHandler above -- the prober is built after the api handler.
	prober searchQualityProber

	// searchProbeTimeout bounds the availability probe on /api/search.
	// Zero means defaultSearchProbeTimeout; tests shorten it.
	searchProbeTimeout time.Duration

	// bbcProbe is the injection point for /api/diag/bbc. Production
	// leaves it nil; the handler falls back to defaultBBCProbe which
	// drives the live IBL client. Tests override it via setBBCProbe
	// to drive happy-path and broken-shape scenarios from in-process
	// fakes without touching the network.
	bbcProbe diagBBCProbe

	// storageProbe / networkProbe / clockHeadDate / nowFn are injection
	// points for the v1.5.7 environment-health diag endpoints
	// (/api/diag/storage, /api/diag/clock, /api/diag/network,
	// /api/diag/geo). Production leaves each nil; the handlers fall
	// back to defaults that perform real filesystem / network / time
	// operations. Tests inject deterministic fakes to drive happy-path
	// and per-failure-mode scenarios without touching real systems.
	// nowFn is shared between A.9 (geo cache age) and A.11 (clock).
	// clockHeadDate is A.11-only.
	storageProbe  diagStoragePathProbe
	networkProbe  diagNetworkHostProbe
	clockHeadDate clockHeadDateFunc
	nowFn         func() time.Time

	// mux is the /api/* router, built once by buildMux and guarded by
	// muxOnce. sync.Once rather than a nil check: the nil-check form is
	// a data race between concurrent first requests, which is exactly
	// what go test -race caught on the abandoned v1.6.0 branch. Once.Do
	// also establishes the happens-before that makes reading h.mux
	// afterwards safe. Deliberately not built in NewHandler so a
	// Handler assembled field by field in a test still routes.
	muxOnce sync.Once
	mux     http.Handler
}

// SetNewznabHandler wires the live newznab handler in so the
// /api/diag/sonarr-handshake endpoint can synthesise a Sonarr
// round-trip against it. Safe to leave unset in tests that don't
// exercise the diag endpoint; the diag handler short-circuits with
// a degraded verdict when newznabHandler is nil.
func (h *Handler) SetNewznabHandler(nzh http.Handler) {
	h.newznabHandler = nzh
}

// SetSABHandler wires the live sabnzbd.Handler in so the
// /api/diag/sab endpoint can probe SAB modes in-process. Safe to
// leave unset in tests; the diag handler degrades gracefully when
// sabHandler is nil.
func (h *Handler) SetSABHandler(sh http.Handler) {
	h.sabHandler = sh
}

// SetProber wires the quality prober in so /api/search can report
// per-result availability. Safe to leave unset: handleSearch reports
// every result as unknown rather than failing the search.
func (h *Handler) SetProber(p searchQualityProber) {
	h.prober = p
}

// RecordIndexerRequest records the current time as the most recent Newznab
// indexer query.  Safe to call from any goroutine.
func (h *Handler) RecordIndexerRequest() {
	h.lastIndexerRequest.Store(time.Now().UTC())
}

// NewHandler creates a new API handler.
func NewHandler(st *store.Store, hub *Hub, mgr *download.Manager, ibl *bbc.IBL, status *RuntimeStatus) *Handler {
	return &Handler{
		store:     st,
		hub:       hub,
		mgr:       mgr,
		ibl:       ibl,
		status:    status,
		StartedAt: time.Now(),
	}
}

// ServeHTTP normalises the path, applies the cross-origin CSRF defence,
// and dispatches through the router built by buildMux. Authentication is
// applied per route by authMiddleware; see registerRoutes for the table
// and unauthenticatedAPIRoutes for the allowlist.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Fold a trailing slash before routing so /api/status/ and
	// /api/status remain the same endpoint, as they were under the
	// pre-mux switch.
	r = normaliseTrailingSlash(r)

	// Cross-origin browser CSRF defence on state-changing methods,
	// kept as defence in depth now that every route below also
	// authenticates. A browser on the same origin sends a matching
	// Origin and passes; a malicious page on another origin sends a
	// mismatched one and is refused.
	if isMutatingMethod(r.Method) && !csrfCheckPasses(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request refused"})
		return
	}

	h.muxOnce.Do(func() { h.mux = h.buildMux() })
	h.mux.ServeHTTP(w, r)
}

// authenticate validates the request against the persisted api_key.
// Three mechanisms are accepted, in order:
//
//  1. `?apikey=<key>` query parameter — Sonarr's default and how /newznab
//     callers are configured.
//  2. `Authorization: Bearer <key>` header — operator-set scripts and
//     dashboards that prefer header-borne secrets to query-string leakage.
//  3. `X-Api-Key: <key>` header — arr-stack convention (Sonarr, Radarr,
//     Lidarr all expose this); widened in v1.5.7 so the test suite and
//     operator habits no longer diverge from production behaviour.
//
// The diag endpoint /api/diag/auth-paths asserts this invariant against
// the running binary so silent drift can't recur.
//
// Every comparison goes through auth.SecretsEqual, which is constant time
// in the number of matching leading bytes. A plain == returns at the first
// differing byte and leaks the key one byte at a time to a caller who can
// measure response latency.
func (h *Handler) authenticate(r *http.Request) bool {
	storedKey, _ := h.store.GetConfig("api_key")
	if storedKey == "" {
		return false
	}

	if key := r.URL.Query().Get("apikey"); auth.SecretsEqual(key, storedKey) {
		return true
	}

	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		token := strings.TrimPrefix(header, "Bearer ")
		if auth.SecretsEqual(token, storedKey) {
			return true
		}
	}

	if key := r.Header.Get("X-Api-Key"); auth.SecretsEqual(key, storedKey) {
		return true
	}

	return false
}

// ResolveDownloadDir returns the active download directory path,
// honouring precedence: env-var > store > default.
//
// The env-derived value (h.DownloadDir, set at startup from DOWNLOAD_DIR)
// always wins. Falls back to the BoltDB-persisted value, then to the
// hardcoded "/downloads" default. Used by handleGetConfig, the
// directory listing handlers, and the SABnzbd compat handler so that
// the download directory shown in the UI and in API responses
// consistently reflects the runtime value.
func (h *Handler) ResolveDownloadDir() string {
	if h.DownloadDir != "" {
		return h.DownloadDir
	}
	if stored, err := h.store.GetConfig("download_dir"); err == nil && stored != "" {
		return stored
	}
	return configDefaults["download_dir"]
}

// writeJSON encodes v as JSON and writes it to the response with the given
// status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// isMutatingMethod returns true for HTTP methods that change server
// state. GET / HEAD / OPTIONS are safe and bypass the CSRF check.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// csrfCheckPasses decides whether a state-changing request may proceed
// past the cross-origin check.
//
// An absent Origin used to pass unconditionally, which made the check
// free to bypass with any non-browser client. It now passes only when the
// caller supplies its credential in a request header. That is not a
// cosmetic distinction: Authorization and X-Api-Key are not CORS-safelisted,
// so a page on another origin cannot set either without a preflight, and
// iplayer-arr answers no preflight and emits no CORS headers, so the
// browser refuses to send the request at all. curl, Sonarr, Radarr and
// operator scripts are unaffected because they set the header anyway.
//
// Presence, not validity, is what is checked here. Validity is the
// authMiddleware's job on the route itself; a forged header value gets
// past this check and straight into a 401.
func csrfCheckPasses(r *http.Request) bool {
	if r.Header.Get("Origin") == "" {
		return hasHeaderCredential(r)
	}
	return sameOriginRequest(r)
}

// hasHeaderCredential reports whether the request carries an API key in a
// request header rather than in the query string.
func hasHeaderCredential(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	return r.Header.Get("X-Api-Key") != ""
}

// sameOriginRequest returns true when the request's Origin header is
// either absent or matches the request's Host. Callers that care about
// the absent case must use csrfCheckPasses instead.
// A browser-issued cross-origin request always sets Origin to the
// scheme+host of the initiating page, so a mismatch is a strong CSRF
// signal. We don't fall back to Referer because some browsers strip
// it under privacy modes; relying on it would create false positives
// for legitimate same-origin requests.
func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	// Parse the origin's host component. "http://foo:62001" -> "foo:62001".
	idx := strings.Index(origin, "://")
	if idx < 0 {
		return false
	}
	originHost := origin[idx+3:]
	if i := strings.IndexByte(originHost, '/'); i >= 0 {
		originHost = originHost[:i]
	}
	return originHost == r.Host
}
