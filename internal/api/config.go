package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

var configKeys = []string{"api_key", "quality", "max_workers", "download_dir", "auto_cleanup", "watchdog_timeout_seconds"}

var configDefaults = map[string]string{
	"quality":                  "any",
	"max_workers":              "4",
	"download_dir":             "/downloads",
	"auto_cleanup":             "false",
	"watchdog_timeout_seconds": "60",
}

func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := make(map[string]string, len(configKeys))
	for _, key := range configKeys {
		val, _ := h.store.GetConfig(key)
		if val == "" {
			val = configDefaults[key]
		}
		cfg[key] = val
	}
	// Override download_dir with the env-derived value if set.
	// See ResolveDownloadDir for the precedence rule.
	cfg["download_dir"] = h.ResolveDownloadDir()
	writeJSON(w, http.StatusOK, cfg)
}

func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	// Cap the request body so a malformed or hostile client can't OOM
	// the process by streaming megabytes of payload. 64 KiB is well
	// above any legitimate config-update size (key + value, both
	// short strings). DisallowUnknownFields rejects payloads that
	// try to wedge extra keys past the JSON decoder. Audit item 22.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	readOnly := map[string]bool{"api_key": true, "download_dir": true}
	if readOnly[req.Key] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": req.Key + " is read-only (set via environment variable)"})
		return
	}
	if req.Key == "max_workers" {
		workers, err := strconv.Atoi(req.Value)
		if err != nil || workers < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_workers must be a positive integer"})
			return
		}
	}

	if err := h.store.SetConfig(req.Key, req.Value); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
