package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/api"
	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/download"
	"github.com/Will-Luck/iplayer-arr/internal/newznab"
	"github.com/Will-Luck/iplayer-arr/internal/sabnzbd"
	"github.com/Will-Luck/iplayer-arr/internal/store"
	"github.com/Will-Luck/iplayer-arr/internal/web"
)

// defaultPort is the TCP port iplayer-arr listens on when the PORT
// environment variable is not set. Chosen to avoid collision with
// FlareSolverr's default port. See
// docs/superpowers/specs/2026-04-08-iplayer-arr-v1.1.1-design.md.
const defaultPort = "62001"

// resolvePort returns the port main() should bind to, applying
// PORT env-var override with fallback to defaultPort.
func resolvePort() string {
	return envOr("PORT", defaultPort)
}

func main() {
	configDir := envOr("CONFIG_DIR", "/config")
	downloadDir := envOr("DOWNLOAD_DIR", "/downloads")
	port := resolvePort()

	dbPath := filepath.Join(configDir, "iplayer-arr.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// seed API key if missing
	apiKey, _ := st.GetConfig("api_key")
	if apiKey == "" {
		b := make([]byte, 16)
		rand.Read(b)
		apiKey = hex.EncodeToString(b)
		st.SetConfig("api_key", apiKey)
	}

	// Normalise legacy "quality" config values to "any" so the v1.4.0
	// ceiling logic doesn't clamp upgraders whose persisted value is no
	// longer a valid option. See GH#39.
	migrateQualityConfig(st)

	// Persist the resolved DOWNLOAD_DIR to the config store on every start.
	// The writer side (download.Manager) uses this value injected directly;
	// the reader side (api/directory.go and sabnzbd/handler.go) reads it
	// back from the store. Without this line the store stays empty and
	// those readers fall back to the hardcoded "/downloads", which diverges
	// from the actual runtime path and breaks the Downloads-page ownership
	// check (Delete button appears inert). See GitHub issue #21.
	st.SetConfig("download_dir", filepath.Clean(downloadDir))

	// purge stale programme cache
	st.PurgeStaleProgrammes(4 * time.Hour)

	// startup health checks
	log.Println("running startup health checks...")

	ffVer, ffErr := download.CheckFFmpeg()
	if ffErr != nil {
		log.Printf("WARNING: ffmpeg not found -- downloads will be disabled: %v", ffErr)
	} else {
		log.Printf("ffmpeg: %s", ffVer)
	}

	bbcClient := bbc.NewClient()
	ibl := bbc.NewIBL(bbcClient)
	ms := bbc.NewMediaSelector(bbcClient)
	playlist := bbc.NewPlaylistResolver(bbcClient)
	probeConcurrency := envIntDefault("IPLAYER_PROBE_CONCURRENCY", 8)
	probeTimeout := time.Duration(envIntDefault("IPLAYER_PROBE_TIMEOUT_SEC", 20)) * time.Second
	prober := bbc.NewQualityProber(playlist, ms, bbcClient, st, probeConcurrency, probeTimeout)
	hub := api.NewHub()
	mgr := download.NewManager(st, downloadDir, configuredMaxWorkers(st), bbcClient, playlist, ms, hub)

	// Start download workers
	workerCtx, workerCancel := context.WithCancel(context.Background())
	mgr.Start(workerCtx)
	go mgr.RunCleanupLoop(workerCtx)

	// Record start time before the geo probe.
	startedAt := time.Now()

	// Geo-probe: check if BBC content is accessible
	geoOK := false
	geoCheckedAt := startedAt.UTC().Format(time.RFC3339)
	bbcStatus, geoErr := bbcClient.Head("https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_hd/format/xml")
	if geoErr != nil {
		log.Printf("WARNING: geo-probe failed: %v", geoErr)
	} else if bbcStatus == 200 {
		geoOK = true
		geoCheckedAt = time.Now().UTC().Format(time.RFC3339)
		log.Println("geo-probe: UK access confirmed")
	} else if bbcStatus == 403 {
		log.Println("WARNING: geo-blocked -- BBC iPlayer content unavailable without a UK connection")
	} else {
		log.Printf("geo-probe: unexpected status %d", bbcStatus)
	}

	if err := download.EnsureDownloadDir(downloadDir); err != nil {
		log.Printf("WARNING: cannot create download dir %s: %v", downloadDir, err)
	}

	// Ring buffer for /api/logs -- write all log output to both stderr and the
	// buffer so recent log lines can be served over HTTP.
	ringBuf := api.NewRingBuffer(1000)
	ringWriter := &ringBufWriter{buf: ringBuf, hub: hub}
	multiWriter := io.MultiWriter(os.Stderr, ringWriter)
	log.SetOutput(multiWriter)
	slog.SetDefault(slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// http routing
	runtimeStatus := &api.RuntimeStatus{
		FFmpegVersion: ffVer,
		GeoOK:         geoOK,
		GeoCheckedAt:  geoCheckedAt,
	}
	apiHandler := api.NewHandler(st, hub, mgr, ibl, runtimeStatus)
	apiHandler.RingBuf = ringBuf
	apiHandler.StartedAt = startedAt
	apiHandler.DownloadDir = downloadDir
	apiHandler.GeoProbe = func() bool {
		status, err := bbcClient.Head("https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_hd/format/xml")
		if err != nil {
			return false
		}
		return status == 200
	}

	mux := http.NewServeMux()
	nzHandler := newznab.NewHandler(ibl, st, ms, prober)
	nzHandler.SetOnRequest(apiHandler.RecordIndexerRequest)
	// Wire the newznab handler into the api handler so the v1.5.6
	// /api/diag/sonarr-handshake endpoint can synthesise a Sonarr
	// round-trip in-process. Ordering matters: apiHandler is built
	// at line 142 before prober/nzHandler exist, so this is a
	// post-construction setter rather than a constructor arg.
	apiHandler.SetNewznabHandler(nzHandler)
	mux.Handle("/newznab/", nzHandler)
	sabHandler := sabnzbd.NewHandler(st, mgr)
	sabHandler.DownloadDir = downloadDir
	apiHandler.SetSABHandler(sabHandler)
	mux.Handle("/sabnzbd/", sabHandler)
	mux.Handle("/api/", apiHandler)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Must be last -- catch-all for SPA routing
	mux.Handle("/", web.SPAHandler())

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// ReadHeaderTimeout protects against slowloris: an attacker
		// keeping a half-open connection by dribbling header bytes.
		ReadHeaderTimeout: 10 * time.Second,
		// IdleTimeout closes keep-alive connections that sit idle so
		// the per-connection goroutine and fd are reclaimed.
		IdleTimeout: 120 * time.Second,
		// WriteTimeout intentionally stays 0 because /api/events is a
		// long-lived SSE stream. Per-route bounds are enforced inside
		// the handlers that need them.
	}

	go func() {
		log.Printf("iplayer-arr listening on :%s", port)
		// Log only a short prefix as a configuration-presence breadcrumb
		// rather than a key-recovery hint. The full key is delivered via
		// the SPA's Settings page (which the operator can authenticate
		// to over LAN). Logs are served unauthenticated at /api/logs, so
		// leaking the suffix as well would reveal 25% of the secret to
		// any LAN visitor. Item 8 / Codex C2 follow-on.
		log.Printf("API key configured (prefix=%s...)", apiKey[:4])
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	// Stop accepting new HTTP first so the worker pool isn't fielding
	// fresh enqueues while it's trying to drain. srv.Shutdown blocks
	// until in-flight requests complete or the context deadline fires.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Now cancel worker contexts (kills ffmpeg children via SIGKILL)
	// and wait for them to release, bounded by waitWithTimeout. A
	// hung ffmpeg that ignores cancel previously blocked main forever
	// on m.wg.Wait(); the bounded wait guarantees the container can
	// exit within ~25s even in that case.
	workerCancel()
	if !waitWithTimeout(mgr.Stop, 15*time.Second) {
		log.Println("warning: download manager did not stop within 15s, exiting anyway")
	}
	log.Println("iplayer-arr stopped")
}

// waitWithTimeout runs fn in a goroutine and waits up to d for it to
// return. Returns true if fn returned, false on timeout. Used by
// main's shutdown path to bound the wait on mgr.Stop's wg.Wait().
func waitWithTimeout(fn func(), d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// ringBufWriter adapts api.RingBuffer to io.Writer for use with log and slog.
// Each Write call is treated as one log line.
type ringBufWriter struct {
	buf *api.RingBuffer
	hub *api.Hub
}

func (rw *ringBufWriter) Write(p []byte) (int, error) {
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	level := detectLevel(msg)
	rw.buf.Add(api.LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   msg,
	}, rw.hub)
	return len(p), nil
}

// detectLevel returns a log level string inferred from the message format.
//
// For slog text output it looks for the "level=LEVEL" key-value pair which
// is unambiguous. For legacy log output it limits the keyword scan to the
// first 80 characters so that level words embedded in the message body (e.g.
// "no error occurred") do not trigger a false positive.
func detectLevel(msg string) string {
	// slog text format: "... level=WARN ..."
	if i := strings.Index(msg, "level="); i >= 0 {
		rest := msg[i+6:]
		if strings.HasPrefix(rest, "ERROR") {
			return "error"
		}
		if strings.HasPrefix(rest, "WARN") {
			return "warn"
		}
		if strings.HasPrefix(rest, "DEBUG") {
			return "debug"
		}
		return "info"
	}
	// Legacy log format: only check for keywords near the start of the line.
	upper := strings.ToUpper(msg[:min(len(msg), 80)])
	if strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL") {
		return "error"
	}
	if strings.Contains(upper, "WARN") {
		return "warn"
	}
	if strings.Contains(upper, "DEBUG") {
		return "debug"
	}
	return "info"
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntDefault(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("invalid %s %q, using default %d", key, raw, fallback)
		return fallback
	}
	return value
}

// validQualityValues mirrors QUALITY_CEILING_OPTIONS in frontend/src/types.ts.
// migrateQualityConfig normalises any persisted "quality" value that isn't
// one of these to "any". Keep in sync with the frontend constant.
var validQualityValues = map[string]bool{
	"any":   true,
	"1080p": true,
	"720p":  true,
	"540p":  true,
	"396p":  true,
}

// migrateQualityConfig normalises legacy or unrecognised "quality" config
// values to "any" so the v1.4.0 ceiling logic in
// internal/newznab/search.go::qualityCeilingHeight does not clamp the RSS
// fallback to a single quality variant per PID for upgraders. Empty values
// are left alone — configDefaults["quality"] = "any" applies at read time.
// Returns true when the value was rewritten. Resolves GH#39.
func migrateQualityConfig(st *store.Store) bool {
	if st == nil {
		return false
	}
	v, _ := st.GetConfig("quality")
	if v == "" {
		return false
	}
	if validQualityValues[strings.ToLower(strings.TrimSpace(v))] {
		return false
	}
	log.Printf("migrating legacy quality config %q -> \"any\" (GH#39)", v)
	if err := st.SetConfig("quality", "any"); err != nil {
		log.Printf("migrate quality config: %v", err)
		return false
	}
	return true
}

func configuredMaxWorkers(st *store.Store) int {
	const defaultMaxWorkers = 4

	if st == nil {
		return defaultMaxWorkers
	}

	raw, _ := st.GetConfig("max_workers")
	if raw == "" {
		return defaultMaxWorkers
	}

	workers, err := strconv.Atoi(raw)
	if err != nil || workers < 1 {
		log.Printf("invalid max_workers %q, using default %d", raw, defaultMaxWorkers)
		return defaultMaxWorkers
	}

	return workers
}
