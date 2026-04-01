package api

import "net/http"

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" || h.ibl == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	writeJSON(w, http.StatusOK, results)
}
