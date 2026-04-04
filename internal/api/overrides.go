package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func (h *Handler) handleListOverrides(w http.ResponseWriter, r *http.Request) {
	overrides, err := h.store.ListOverrides()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if overrides == nil {
		overrides = []*store.ShowOverride{}
	}
	writeJSON(w, http.StatusOK, overrides)
}

func (h *Handler) handlePutOverride(w http.ResponseWriter, r *http.Request) {
	showName := extractOverrideName(r.URL.Path)
	if showName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing show name"})
		return
	}

	var o store.ShowOverride
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	o.ShowName = showName

	if err := h.store.PutOverride(&o); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	showName := extractOverrideName(r.URL.Path)
	if showName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing show name"})
		return
	}

	if err := h.store.DeleteOverride(showName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// extractOverrideName pulls and URL-decodes the show name from the path.
func extractOverrideName(path string) string {
	raw := strings.TrimPrefix(path, "/api/overrides/")
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}
