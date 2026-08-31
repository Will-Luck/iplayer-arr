package api

import (
	"net/http"
	"strings"
)

// routeSpec is one entry in the /api/* route table.
//
// Method is the HTTP method the pattern is registered for, or "" for a
// pattern that answers every method. Pattern is the http.ServeMux pattern
// without the method prefix. Authenticated records whether the route was
// wrapped in authMiddleware.
type routeSpec struct {
	Method        string
	Pattern       string
	Authenticated bool
}

// routeRegistry is the only path by which a route reaches the mux.
//
// Every registration is recorded, which is what lets the route-population
// test enumerate the real surface instead of a list someone has to
// remember to update. registerRoutes never touches the mux directly, so a
// new endpoint cannot exist without a routeSpec describing it.
type routeRegistry struct {
	handler *Handler
	mux     *http.ServeMux
	routes  []routeSpec
}

// handle registers one route and records it. When authenticated is true
// the handler is wrapped in authMiddleware; when it is false the pattern
// must appear in unauthenticatedAPIRoutes or the population test fails.
func (rr *routeRegistry) handle(method, pattern string, fn http.HandlerFunc, authenticated bool) {
	rr.routes = append(rr.routes, routeSpec{
		Method:        method,
		Pattern:       pattern,
		Authenticated: authenticated,
	})

	var h http.Handler = fn
	if authenticated {
		h = authMiddleware(rr.handler, h)
	}
	if method == "" {
		rr.mux.Handle(pattern, h)
		return
	}
	rr.mux.Handle(method+" "+pattern, h)
}

// unauthenticatedAPIRoutes is the complete allowlist of /api/* patterns
// reachable without a credential, with the reason each one is there.
//
// Adding an entry here is the only way to ship an unauthenticated route,
// and the population test checks the list in both directions: no
// unauthenticated route may be missing from it, and no entry may name a
// pattern that is no longer registered.
var unauthenticatedAPIRoutes = map[string]string{
	"/api/healthz": "Liveness probe for Docker HEALTHCHECK, Watchtower and external monitoring. " +
		"Returns a fixed {\"ok\":true} and reads no operator state, so it discloses nothing " +
		"beyond the fact that the process is up, which anyone who can open the TCP port " +
		"already knows. It also replaces GET /api/status as the readiness poll in CI and " +
		"the smoke test, because /api/status reports disk capacity, VPN geo posture and the " +
		"ffmpeg build and must therefore authenticate.",
	"/api/": "Not-found catch-all for unknown /api/ paths. Answering 401 here would turn the " +
		"endpoint into a route oracle: an unauthenticated caller could tell a real gated " +
		"route from a typo by the status code. It returns 404 and reaches no handler that " +
		"touches operator state.",
	"/api": "The same not-found catch-all for the bare /api path. Registered explicitly so " +
		"that trailing-slash normalisation, which rewrites /api/ to /api, still lands on our " +
		"JSON 404 rather than the ServeMux default plain-text one.",
}

// registerRoutes declares the whole /api/* surface.
//
// Ordering in this function has no effect on dispatch: http.ServeMux picks
// the most specific matching pattern, so /api/downloads/directory/{folder...}
// wins over /api/downloads/{id...} regardless of registration order.
func (h *Handler) registerRoutes(rr *routeRegistry) {
	// Liveness. The only route that answers without a credential.
	rr.handle("GET", "/api/healthz", h.handleHealthz, false)

	// Server-sent events. Authenticated like everything else; the
	// credential arrives as ?apikey= because EventSource cannot set
	// headers. See authMiddleware.
	rr.handle("GET", "/api/events", h.handleEvents, true)

	// Status and system.
	rr.handle("GET", "/api/status", h.handleStatus, true)
	rr.handle("GET", "/api/system", h.handleSystem, true)
	rr.handle("POST", "/api/system/geo-check", h.handleGeoCheck, true)
	rr.handle("GET", "/api/logs", h.handleLogs, true)

	// Downloads.
	rr.handle("GET", "/api/downloads", h.handleListDownloads, true)
	rr.handle("GET", "/api/downloads/directory", h.handleListDirectory, true)
	rr.handle("DELETE", "/api/downloads/directory/{folder...}", h.handleDeleteDirectory, true)
	rr.handle("DELETE", "/api/downloads/{id...}", h.handleCancelDownload, true)
	rr.handle("POST", "/api/download", h.handleManualDownload, true)
	rr.handle("POST", "/api/pause", h.handlePause, true)
	rr.handle("POST", "/api/resume", h.handleResume, true)

	// History.
	rr.handle("GET", "/api/history", h.handleListHistory, true)
	rr.handle("GET", "/api/history/stats", h.handleHistoryStats, true)
	rr.handle("DELETE", "/api/history", h.handleClearHistory, true)
	rr.handle("DELETE", "/api/history/{id...}", h.handleDeleteHistory, true)

	// Config.
	rr.handle("GET", "/api/config", h.handleGetConfig, true)
	rr.handle("PUT", "/api/config", h.handlePutConfig, true)

	// Overrides.
	rr.handle("GET", "/api/overrides", h.handleListOverrides, true)
	rr.handle("PUT", "/api/overrides/{name...}", h.handlePutOverride, true)
	rr.handle("DELETE", "/api/overrides/{name...}", h.handleDeleteOverride, true)

	// Search.
	rr.handle("GET", "/api/search", h.handleSearch, true)

	// Diagnostics. Each of these handlers also calls h.authenticate
	// inline; that predates the middleware and is left in place as
	// defence in depth against a future refactor unwrapping a route.
	rr.handle("GET", "/api/diag/sonarr-handshake", h.handleDiagSonarrHandshake, true)
	rr.handle("GET", "/api/diag/ffmpeg", h.handleDiagFfmpeg, true)
	rr.handle("GET", "/api/diag/bbc", h.handleDiagBBC, true)
	rr.handle("GET", "/api/diag/sab", h.handleDiagSAB, true)
	rr.handle("GET", "/api/diag/auth-paths", h.handleDiagAuthPaths, true)
	rr.handle("GET", "/api/diag/storage", h.handleDiagStorage, true)
	rr.handle("GET", "/api/diag/clock", h.handleDiagClock, true)
	rr.handle("GET", "/api/diag/geo", h.handleDiagGeo, true)
	rr.handle("GET", "/api/diag/network", h.handleDiagNetwork, true)

	// Unknown /api paths. Must be registered last in reading order for
	// human clarity only; see the note on registerRoutes about ordering.
	rr.handle("", "/api/", h.handleNotFound, false)
	rr.handle("", "/api", h.handleNotFound, false)
}

// buildMux constructs the live router. Called once per Handler, guarded by
// a sync.Once in ServeHTTP: the previous lazy `if h.muxCache == nil` form
// was a data race between concurrent requests, which is what go test -race
// reported on the abandoned v1.6.0 branch.
func (h *Handler) buildMux() http.Handler {
	rr := &routeRegistry{handler: h, mux: http.NewServeMux()}
	h.registerRoutes(rr)
	return rr.mux
}

// routeSpecs returns the route table the live mux is built from. Tests use
// it to enumerate the real population; it runs the same registerRoutes the
// router runs, against a throwaway mux.
func (h *Handler) routeSpecs() []routeSpec {
	rr := &routeRegistry{handler: h, mux: http.NewServeMux()}
	h.registerRoutes(rr)
	return rr.routes
}

// normaliseTrailingSlash returns a request whose path has one trailing
// slash removed, leaving the original untouched.
//
// The pre-mux router did this with strings.TrimSuffix on every request, so
// /api/status/ and /api/status were the same endpoint. http.ServeMux does
// not: an exact pattern does not match a trailing slash, so about ten paths
// would silently fall through to the /api/ catch-all and start answering
// 404. The request and its URL are shallow-copied because a handler further
// down (or the server itself) may still read the original.
func normaliseTrailingSlash(r *http.Request) *http.Request {
	p := r.URL.Path
	if len(p) <= 1 || !strings.HasSuffix(p, "/") {
		return r
	}

	u := *r.URL
	u.Path = strings.TrimSuffix(p, "/")
	// RawPath, when set, is what ServeMux matches on. Leaving it stale
	// would undo the trim for any path containing an escaped character.
	if u.RawPath != "" {
		u.RawPath = strings.TrimSuffix(u.RawPath, "/")
	}
	r2 := *r
	r2.URL = &u
	return &r2
}

// handleHealthz is the unauthenticated liveness probe. It deliberately
// reports nothing about the instance beyond "the process is answering".
func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleNotFound preserves the JSON 404 shape the pre-mux router returned
// for unknown /api paths.
func (h *Handler) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// handlePause pauses the download manager. Extracted from the inline body
// in the old ServeHTTP switch so the route can be registered and wrapped.
func (h *Handler) handlePause(w http.ResponseWriter, r *http.Request) {
	h.mgr.Pause()
	writeJSON(w, http.StatusOK, map[string]bool{"paused": true})
}

// handleResume resumes the download manager.
func (h *Handler) handleResume(w http.ResponseWriter, r *http.Request) {
	h.mgr.Resume()
	writeJSON(w, http.StatusOK, map[string]bool{"paused": false})
}
