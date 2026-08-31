package api

import "net/http"

// authMiddleware rejects a request that carries no valid API key before it
// reaches the wrapped handler.
//
// It delegates to Handler.authenticate, so the three accepted mechanisms
// (?apikey=, Authorization: Bearer, X-Api-Key) stay defined in exactly one
// place and /api/diag/auth-paths keeps asserting them against the running
// binary. The query-parameter form has to stay accepted here because
// /api/events is an EventSource stream and EventSource cannot set request
// headers; refusing it would silence every live dashboard update. It costs
// nothing extra in exposure, because /newznab/ and /sabnzbd/ already accept
// the same key in the query string for Sonarr and Radarr.
//
// The 401 body matches the shape the diag handlers already emit so the SPA
// can read err.error uniformly.
func authMiddleware(h *Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.authenticate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
