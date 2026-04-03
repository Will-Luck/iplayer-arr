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

	"github.com/GiteaLN/iplayer-arr/internal/newznab"
	"github.com/GiteaLN/iplayer-arr/internal/store"
)

type DownloadStarter interface {
	StartDownload(pid, quality, title, category string) (string, error)
	CancelDownload(nzoID string) error
	IsPaused() bool
}

type Handler struct {
	store   *store.Store
	starter DownloadStarter
}

func NewHandler(st *store.Store, starter DownloadStarter) *Handler {
	return &Handler{store: st, starter: starter}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	log.Printf("[sabnzbd] %s %s mode=%s params=%s", r.Method, r.URL.Path, mode, r.URL.RawQuery)

	switch mode {
	case "version":
		writeJSON(w, map[string]interface{}{"version": "4.0.0"})
		return
	case "get_cats":
		writeJSON(w, map[string]interface{}{"categories": []string{"sonarr", "tv", "manual"}})
		return
	case "get_config":
		downloadDir, _ := h.store.GetConfig("download_dir")
		if downloadDir == "" {
			downloadDir = "/downloads"
		}
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
		return
	case "fullstatus":
		downloadDir, _ := h.store.GetConfig("download_dir")
		if downloadDir == "" {
			downloadDir = "/downloads"
		}
		writeJSON(w, map[string]interface{}{
			"status": map[string]interface{}{
				"completedir": downloadDir,
			},
		})
		return
	}

	// all other modes require auth
	apiKey := r.URL.Query().Get("apikey")
	storedKey, _ := h.store.GetConfig("api_key")
	if apiKey != storedKey {
		writeJSON(w, map[string]interface{}{
			"status": false,
			"error":  "API Key Incorrect",
		})
		return
	}

	switch mode {
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
		if h.starter != nil {
			h.starter.CancelDownload(value)
		}
		// Move to history instead of hard-deleting so Sonarr can still
		// see the Completed entry. If the download is already gone
		// (e.g. worker moved it first), just delete from both buckets.
		if err := h.store.MoveToHistory(value); err != nil {
			h.store.DeleteDownload(value)
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
			"download_time": int(dl.CompletedAt.Sub(dl.StartedAt).Seconds()),
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
