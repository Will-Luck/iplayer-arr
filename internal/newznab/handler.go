package newznab

import (
	"context"
	"net/http"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

type qualityProber interface {
	PrefetchPIDs(ctx context.Context, items []bbc.ProbeItem) bbc.PrefetchResult
}

type Handler struct {
	ibl    *bbc.IBL
	store  *store.Store
	ms     *bbc.MediaSelector
	prober qualityProber // NEW — v1.1.0 quality probe prefetch
	// onRequest, when non-nil, is called on every Newznab request so that the
	// caller can track the last indexer query time.
	onRequest func()
}

func NewHandler(ibl *bbc.IBL, st *store.Store, ms *bbc.MediaSelector, prober qualityProber) *Handler {
	return &Handler{
		ibl:    ibl,
		store:  st,
		ms:     ms,
		prober: prober,
	}
}

// SetOnRequest registers a callback that is invoked at the start of every
// Newznab request.  Intended for recording LastIndexerRequest timestamps.
func (h *Handler) SetOnRequest(fn func()) {
	h.onRequest = fn
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.onRequest != nil {
		h.onRequest()
	}

	t := r.URL.Query().Get("t")

	// t=caps is the only operation that stays unauthenticated. Newznab
	// clients (Sonarr) probe caps to learn the server's supported
	// parameters BEFORE attaching the apikey. Every other operation
	// handles content and must be guarded so anyone on the LAN can't
	// enumerate programmes or pull NZBs without the key.
	if t != "caps" && !h.authenticate(r) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><error code="100" description="Invalid API Key"/>`))
		return
	}

	switch t {
	case "caps":
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(capsXML()))
	case "search":
		h.handleSearch(w, r)
	case "tvsearch":
		h.handleTVSearch(w, r)
	case "get":
		h.handleGet(w, r)
	default:
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><error code="202" description="No such function"/>`))
	}
}

// authenticate compares the request's apikey query param,
// Authorization: Bearer header, or X-Api-Key header to the value
// persisted in the store. A nil store, or a stored key that hasn't
// been seeded yet, short-circuits to true. Production main.go seeds
// the api_key on every startup before the HTTP server begins accepting
// connections, so the unseeded case is only ever hit in tests that
// build a real store but don't bother to write the key. Defence: the
// operator should never see an unseeded key in production logs.
//
// X-Api-Key support is the arr-stack convention (Sonarr, Radarr, Lidarr
// all expose it). Widened in v1.5.7 to match the api.Handler.authenticate
// contract; see /api/diag/auth-paths for the running-binary assertion.
func (h *Handler) authenticate(r *http.Request) bool {
	if h.store == nil {
		return true
	}
	storedKey, _ := h.store.GetConfig("api_key")
	if storedKey == "" {
		return true
	}
	if key := r.URL.Query().Get("apikey"); key != "" && key == storedKey {
		return true
	}
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " && auth[7:] == storedKey {
		return true
	}
	if key := r.Header.Get("X-Api-Key"); key == storedKey {
		return true
	}
	return false
}
