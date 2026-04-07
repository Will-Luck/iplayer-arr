package newznab

import (
	"context"
	"net/http"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

type qualityProber interface {
	PrefetchPIDs(ctx context.Context, items []bbc.ProbeItem) map[string][]int
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
