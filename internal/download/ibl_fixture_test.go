package download

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeIBLEpisodes stands in for iBL's /episodes/{pid}: whatever pid is
// asked for gets a one-version episode carrying vpid and duration (ISO
// 8601), and any other path is a 404, so a resolver that asks the wrong
// endpoint fails instead of being handed a body. GitHub #57.
func fakeIBLEpisodes(t *testing.T, title, vpid, duration string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pid, ok := strings.CutPrefix(r.URL.Path, "/episodes/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"episodes": []map[string]interface{}{{
				"id":    pid,
				"live":  false,
				"title": title,
				"versions": []map[string]interface{}{{
					"id":       vpid,
					"kind":     "original",
					"duration": map[string]string{"value": duration},
				}},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}
