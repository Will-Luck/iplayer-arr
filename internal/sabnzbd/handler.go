package sabnzbd

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/newznab"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

type DownloadStarter interface {
	StartDownload(pid, quality, title, category string) (string, error)
	CancelDownload(nzoID string) error
	IsPaused() bool
}

type Handler struct {
	store       *store.Store
	starter     DownloadStarter
	DownloadDir string // env-derived; if empty, falls back to store
}

func NewHandler(st *store.Store, starter DownloadStarter) *Handler {
	return &Handler{store: st, starter: starter}
}

func sanitiseQuery(raw string) string {
	if !strings.Contains(raw, "apikey=") {
		return raw
	}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if !params.Has("apikey") {
		return raw
	}
	params.Del("apikey")
	encoded := params.Encode()
	if encoded == "" {
		return "apikey=***"
	}
	return encoded + "&apikey=***"
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	log.Printf("[sabnzbd] %s %s mode=%s params=%s", r.Method, r.URL.Path, mode, sanitiseQuery(r.URL.RawQuery))

	// mode=version is the only operation that stays unauthenticated.
	// Sonarr probes version BEFORE attaching the apikey so it can
	// recognise the SABnzbd dialect — same shape as the Newznab
	// t=caps probe. Every other mode reads or writes operator state
	// (download directory, category list, queue, history) and must
	// be guarded so anyone on the LAN can't enumerate or modify
	// downloads without the key. See `internal/newznab/handler.go`
	// for the parallel structure.
	if mode != "version" {
		apiKey := r.URL.Query().Get("apikey")
		storedKey, _ := h.store.GetConfig("api_key")
		if storedKey != "" && apiKey != storedKey {
			writeJSON(w, map[string]interface{}{
				"status": false,
				"error":  "API Key Incorrect",
			})
			return
		}
	}

	switch mode {
	case "version":
		writeJSON(w, map[string]interface{}{"version": "4.0.0"})
	case "get_cats":
		writeJSON(w, map[string]interface{}{"categories": []string{"sonarr", "tv", "manual"}})
	case "get_config":
		downloadDir := h.ResolveDownloadDir()
		writeJSON(w, map[string]interface{}{
			"config": map[string]interface{}{
				"misc": map[string]interface{}{
					"complete_dir": downloadDir,
				},
				"categories": []map[string]interface{}{
					{"name": "sonarr", "dir": ""},
					{"name": "tv", "dir": ""},
					{"name": "manual", "dir": ""},
				},
			},
		})
	case "fullstatus":
		downloadDir := h.ResolveDownloadDir()
		writeJSON(w, map[string]interface{}{
			"status": map[string]interface{}{
				"completedir": downloadDir,
			},
		})
	case "queue":
		h.handleQueue(w, r)
	case "history":
		h.handleHistory(w, r)
	case "addurl", "addfile":
		h.handleAdd(w, r)
	default:
		writeJSON(w, map[string]interface{}{"status": false, "error": "unknown mode"})
	}
}

func (h *Handler) handleQueue(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "delete" {
		value := r.URL.Query().Get("value")
		// Snapshot the row BEFORE routing through CancelDownload. The
		// manager's CancelDownload (introduced in v1.4.1 for audit item
		// 3) ends with DeleteDownload, after which MoveToHistory would
		// always fail. Sonarr's SAB protocol expects to see deleted
		// entries in history with a terminal status so it can move on
		// to the next release; without this preservation Sonarr keeps
		// rediscovering the same release on every RSS sync. Audit item
		// 20.
		snapshot, _ := h.store.GetDownload(value)
		if h.starter != nil {
			h.starter.CancelDownload(value)
		}
		if snapshot != nil {
			snapshot.Status = store.StatusFailed
			if snapshot.Error == "" {
				snapshot.Error = "cancelled via SAB API"
			}
			snapshot.CompletedAt = time.Now()
			if err := h.store.PutHistory(snapshot); err != nil {
				log.Printf("[sabnzbd] queue delete: put history %s: %v", value, err)
			}
		}
		writeJSON(w, map[string]interface{}{"status": true})
		return
	}

	downloads, _ := h.store.ListDownloads()
	var slots []map[string]interface{}
	for _, dl := range downloads {
		if dl.Status == store.StatusFailed && !dl.Retryable {
			continue
		}

		status := "Queued"
		switch dl.Status {
		case store.StatusDownloading, store.StatusConverting:
			status = "Downloading"
		case store.StatusCompleted:
			status = "Completed"
		case store.StatusResolving:
			status = "Queued"
		case store.StatusFailed:
			status = "Queued"
		}

		mbTotal := float64(dl.Size) / 1024 / 1024
		mbLeft := mbTotal * (1 - dl.Progress/100)

		slots = append(slots, map[string]interface{}{
			"nzo_id":     dl.ID,
			"filename":   dl.Title,
			"status":     status,
			"percentage": fmt.Sprintf("%.0f", dl.Progress),
			"mb":         fmt.Sprintf("%.2f", mbTotal),
			"mbleft":     fmt.Sprintf("%.2f", mbLeft),
			"timeleft":   "0:00:00",
			"cat":        dl.Category,
			"size":       fmt.Sprintf("%.2f MB", mbTotal),
			"sizeleft":   fmt.Sprintf("%.2f MB", mbLeft),
		})
	}

	if slots == nil {
		slots = []map[string]interface{}{}
	}

	paused := h.starter != nil && h.starter.IsPaused()
	resp := map[string]interface{}{
		"queue": map[string]interface{}{
			"status":    "Downloading",
			"paused":    paused,
			"noofslots": len(slots),
			"speed":     "0",
			"timeleft":  "0:00:00",
			"slots":     slots,
		},
	}
	for _, s := range slots {
		log.Printf("[sabnzbd] queue slot: id=%s status=%s file=%q cat=%s", s["nzo_id"], s["status"], s["filename"], s["cat"])
	}
	writeJSON(w, resp)
}

func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "delete" {
		value := r.URL.Query().Get("value")
		h.store.DeleteHistory(value)
		writeJSON(w, map[string]interface{}{"status": true})
		return
	}

	history, _ := h.store.ListHistory()

	// Also include completed downloads still in the downloads bucket (waiting
	// for Sonarr to see them before moving to history)
	downloads, _ := h.store.ListDownloads()
	for _, dl := range downloads {
		if dl.Status == store.StatusCompleted {
			history = append(history, dl)
		}
	}

	var slots []map[string]interface{}
	for _, dl := range history {
		status := "Completed"
		if dl.Status == store.StatusFailed {
			status = "Failed"
		}
		slots = append(slots, map[string]interface{}{
			"nzo_id":        dl.ID,
			"name":          dl.Title,
			"nzb_name":      dl.Title + ".nzb",
			"status":        status,
			"storage":       dl.OutputDir,
			"path":          dl.OutputDir,
			"bytes":         dl.Size,
			"downloaded":    dl.Size,
			"completed":     dl.CompletedAt.Unix(),
			"download_time": downloadSeconds(dl.StartedAt, dl.CompletedAt),
			"category":      dl.Category,
			"fail_message":  dl.Error,
			"action_line":   "",
			"script":        "None",
		})
	}

	if slots == nil {
		slots = []map[string]interface{}{}
	}

	for _, s := range slots {
		log.Printf("[sabnzbd] history slot: id=%s status=%s name=%q storage=%q category=%s", s["nzo_id"], s["status"], s["name"], s["storage"], s["category"])
	}
	writeJSON(w, map[string]interface{}{
		"history": map[string]interface{}{
			"slots": slots,
		},
	})
}

// downloadSeconds returns whole seconds spent downloading, guarding an unset
// StartedAt: CompletedAt.Sub(zeroTime) saturates to math.MaxInt64, and the
// resulting ~9.2e9 overflows the Int32 Sonarr parses download_time into,
// breaking the whole history response. Entries that never started report 0.
func downloadSeconds(startedAt, completedAt time.Time) int {
	if startedAt.IsZero() || completedAt.IsZero() || completedAt.Before(startedAt) {
		return 0
	}
	return int(completedAt.Sub(startedAt).Seconds())
}

func (h *Handler) handleAdd(w http.ResponseWriter, r *http.Request) {
	log.Printf("[sabnzbd] handleAdd called: mode=%s method=%s nzbname=%q cat=%s", r.URL.Query().Get("mode"), r.Method, r.URL.Query().Get("nzbname"), r.URL.Query().Get("cat"))
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

	category := r.URL.Query().Get("cat")
	if category == "" {
		category = "sonarr"
	}

	pid, quality, nzbFilename, err := h.extractFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"status": false,
			"error":  err.Error(),
		})
		return
	}

	title := r.URL.Query().Get("nzbname")
	if title == "" {
		title = nzbFilename
	}
	if title == "" {
		title = pid
	}
	log.Printf("[sabnzbd] download title: %q pid: %s quality: %s", title, pid, quality)

	if h.starter == nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "downloads disabled"})
		return
	}

	id, err := h.starter.StartDownload(pid, quality, title, category)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  true,
		"nzo_ids": []string{id},
	})
}

func (h *Handler) extractFromRequest(r *http.Request) (pid, quality, nzbFilename string, err error) {
	mode := r.URL.Query().Get("mode")

	if mode == "addfile" {
		// Primary path: Sonarr uploads NZB as multipart file
		file, fh, fErr := r.FormFile("name")
		if fErr != nil {
			return "", "", "", fmt.Errorf("read NZB file: %w", fErr)
		}
		if fh != nil {
			nzbFilename = strings.TrimSuffix(fh.Filename, ".nzb")
		}
		defer file.Close()

		data, fErr := io.ReadAll(file)
		if fErr != nil {
			return "", "", "", fmt.Errorf("read NZB data: %w", fErr)
		}
		pid, quality, err = parseNZBSegment(data)
		return pid, quality, nzbFilename, err
	}

	// Fallback: addurl -- Sonarr sends URL pointing to our t=get endpoint
	nzbURL := r.URL.Query().Get("name")
	if nzbURL == "" {
		return "", "", "", fmt.Errorf("missing name parameter")
	}
	pid, quality, err = parseNZBURL(nzbURL)
	return pid, quality, "", err
}

func parseNZBSegment(nzbData []byte) (pid, quality string, err error) {
	var nzb struct {
		Files []struct {
			Segments []struct {
				Text string `xml:",chardata"`
			} `xml:"segments>segment"`
		} `xml:"file"`
	}
	if err := xml.Unmarshal(nzbData, &nzb); err != nil {
		return "", "", fmt.Errorf("parse NZB: %w", err)
	}
	for _, f := range nzb.Files {
		for _, seg := range f.Segments {
			parts := strings.SplitN(seg.Text, ":", 2)
			if len(parts) == 2 {
				return parts[0], parts[1], nil
			}
		}
	}
	return "", "", fmt.Errorf("no download segment found in NZB")
}

func parseNZBURL(nzbURL string) (pid, quality string, err error) {
	u, uErr := url.Parse(nzbURL)
	if uErr != nil {
		return "", "", fmt.Errorf("parse NZB URL: %w", uErr)
	}
	guid := u.Query().Get("id")
	if guid == "" {
		return "", "", fmt.Errorf("no id in NZB URL")
	}
	info, dErr := newznab.DecodeGUID(guid)
	if dErr != nil {
		return "", "", fmt.Errorf("decode GUID: %w", dErr)
	}
	return info.PID, info.Quality, nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ResolveDownloadDir returns the active download directory path,
// honouring precedence: env-var > store > default. Mirrors the helper
// on the api.Handler so the SABnzbd compat endpoint reports the same
// directory the rest of the app uses.
func (h *Handler) ResolveDownloadDir() string {
	if h.DownloadDir != "" {
		return h.DownloadDir
	}
	if stored, err := h.store.GetConfig("download_dir"); err == nil && stored != "" {
		return stored
	}
	return "/downloads"
}
