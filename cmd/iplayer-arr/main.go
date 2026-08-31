package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
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

// apiKeyFileName is the file, inside CONFIG_DIR, that the effective API
// key is written to on every start. It is the supported out-of-band
// delivery channel for the key: readable over the /config Docker volume
// or with `docker exec <container> cat /config/api_key`, and never
// served over HTTP (internal/web only serves the embedded SPA bundle).
const apiKeyFileName = "api_key"

// minOperatorAPIKeyLen is the shortest API_KEY an operator may supply.
// The generated key is 32 hex characters (128 bits of entropy); one
// value guards the dashboard API, the Newznab indexer and the SABnzbd
// download client, so a handful of characters is brute-forceable from
// the LAN in minutes. A short value is refused with an explanation
// rather than accepted or silently ignored.
const minOperatorAPIKeyLen = 16

// apiKeyLogPrefixLen is how much of the key the startup breadcrumb may
// reveal, and apiKeyLogMinLen is the length below which it reveals none.
const (
	apiKeyLogPrefixLen = 4
	apiKeyLogMinLen    = 12
)

// apiKeyLogPrefix returns the fragment of the key the startup log may
// carry, or "" when the key is too short for any fragment to be safe.
//
// Two reasons this is a function rather than an inline apiKey[:4].
// First, slicing a shorter key panics, and that panic fired in the
// goroutine that calls ListenAndServe, so the process died before it
// ever bound and the container crash-looped. Second, /api/logs serves
// the ring buffer, so four characters of a 32-character key is a
// breadcrumb while four characters of a short one is a real share of
// the secret. A short key can still reach here from the store, written
// by an older build, even though API_KEY now refuses one.
func apiKeyLogPrefix(key string) string {
	if len(key) < apiKeyLogMinLen {
		return ""
	}
	return key[:apiKeyLogPrefixLen]
}

// resolvePort returns the port main() should bind to, applying
// PORT env-var override with fallback to defaultPort.
func resolvePort() string {
	return envOr("PORT", defaultPort)
}

// resolveListenAddr returns the TCP address the HTTP server binds to.
// BIND_ADDR defaults to the empty string, which means every interface and
// reproduces the historical behaviour exactly. Setting it to 127.0.0.1
// confines the listener to loopback, which is what a host running the
// binary directly (rather than in a published container) wants.
func resolveListenAddr() string {
	return net.JoinHostPort(envOr("BIND_ADDR", ""), resolvePort())
}

// resolveAPIKey returns the effective API key, applying the precedence
// API_KEY env var > value persisted in BoltDB > freshly generated
// 128-bit hex. The resolved value is always written back to the store
// because the Newznab and SABnzbd handlers authenticate against the
// store, not against this variable; skipping the write would let an
// operator-supplied API_KEY authenticate the dashboard while Sonarr
// still needed the old generated key.
//
// A whitespace-only API_KEY is treated as unset. Compose files routinely
// carry `API_KEY=` or `API_KEY=" "` placeholders, and honouring those
// literally would blank the key and lock the operator out.
//
// A non-blank API_KEY shorter than minOperatorAPIKeyLen is refused, and
// refused BEFORE the store is written: persisting it and then failing
// would leave the short key behind, so the next start without API_KEY
// would silently adopt it.
//
// A short key already in the store is left alone. Rejecting it would
// lock an operator out of a working install over a value they cannot
// change without the dashboard the value guards.
func resolveAPIKey(st *store.Store) (string, error) {
	if env := strings.TrimSpace(os.Getenv("API_KEY")); env != "" {
		if len(env) < minOperatorAPIKeyLen {
			return "", fmt.Errorf(
				"API_KEY is %d characters long but must be at least %d; "+
					"supply a longer value, or unset API_KEY to have a 32-character key generated for you",
				len(env), minOperatorAPIKeyLen)
		}
		if err := st.SetConfig("api_key", env); err != nil {
			return "", err
		}
		return env, nil
	}
	if existing, _ := st.GetConfig("api_key"); existing != "" {
		return existing, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b)
	if err := st.SetConfig("api_key", key); err != nil {
		return "", err
	}
	return key, nil
}

// writeAPIKeyFile writes the effective key to <dir>/api_key with 0600
// permissions and returns the path it used.
//
// This is the replacement for reading the key out of GET /api/config,
// which used to be unauthenticated. The key cannot go to the log
// instead: /api/logs serves the ring buffer, so a logged key would just
// relocate the disclosure.
//
// The write is staged through a fresh temp file in the same directory
// and then renamed over the target. Writing the target in place was
// wrong three ways:
//
//   - os.WriteFile does not change the mode of a file that already
//     exists, so over a 0644 file the plaintext key was world-readable
//     for the window between the write and the chmod that followed it.
//   - On a mount that refuses chmod, that window never closed, and the
//     function returned an error after the key had already landed, so
//     main logged that the file could not be written when it had been.
//   - Neither O_EXCL nor O_NOFOLLOW was set, so a symlink planted at the
//     path gave an attacker truncate-plus-chmod on any file the
//     container uid could reach.
//
// os.CreateTemp opens with O_CREATE|O_EXCL on a name that does not exist
// yet, so there is no file to follow and no pre-existing mode to inherit.
// The mode is set before the key is written, so the secret never exists
// on disk under a wider mode. rename(2) replaces a symlink at the
// destination rather than following it, and is atomic, so a concurrent
// reader sees either the old file or the new one and never a partial
// key. Every failure path leaves the target untouched, which is what
// makes main's warning truthful.
func writeAPIKeyFile(dir, key string) (string, error) {
	path := filepath.Join(dir, apiKeyFileName)

	tmp, err := os.CreateTemp(dir, "."+apiKeyFileName+"-*")
	if err != nil {
		return path, err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup. Harmless after a successful rename, because
	// the temp name no longer exists.
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return path, err
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return path, err
	}
	if err := tmp.Close(); err != nil {
		return path, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return path, err
	}
	return path, nil
}

func main() {
	configDir := envOr("CONFIG_DIR", "/config")
	downloadDir := envOr("DOWNLOAD_DIR", "/downloads")

	dbPath := filepath.Join(configDir, "iplayer-arr.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Resolve the API key (API_KEY env > persisted > generated) and drop a
	// 0600 copy in the config directory. The file is how the operator gets
	// the key now that the dashboard API is authenticated; see README.
	apiKey, err := resolveAPIKey(st)
	if err != nil {
		log.Fatalf("invalid API key configuration: %v", err)
	}
	keyFilePath, keyFileErr := writeAPIKeyFile(configDir, apiKey)
	if keyFileErr != nil {
		// Not fatal, but note that a read-only CONFIG_DIR is NOT the
		// case this covers: store.Open above needs to write the BoltDB
		// file and log.Fatalfs first, so the process never reaches here
		// on a read-only mount. What this covers is a directory that
		// accepts the database but not this write: a full or
		// quota-exhausted filesystem, a mount whose options refuse the
		// chmod, or an immutable/SELinux-restricted path. In every one
		// of those the service itself is fine and the operator can
		// still pin the key with API_KEY, so refusing to boot would
		// take the download queue down for a delivery-channel problem.
		//
		// The write is atomic, so this warning is truthful: on any
		// error the previous file, if any, is untouched.
		log.Printf("WARNING: could not write API key file %s: %v", keyFilePath, keyFileErr)
		log.Printf("Set the API_KEY environment variable to pin a known key instead")
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

	// purge first-seen feed stamps so the bucket cannot grow without
	// bound; standard iPlayer availability windows run to 12 months,
	// so 400 days covers them with margin (#47)
	if n, err := st.PurgeStaleFirstSeen(400 * 24 * time.Hour); err != nil {
		log.Printf("WARNING: first-seen purge failed: %v", err)
	} else if n > 0 {
		log.Printf("purged %d stale first-seen entries", n)
	}

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
	mgr := download.NewManager(st, downloadDir, configuredMaxWorkers(st),
		bbcClient, playlist, ms, hub,
		download.WithWatchdogTimeout(configuredWatchdogTimeout(st)),
		download.WithAdaptiveThrottle(configuredAdaptiveThrottle(st)))

	// Start download workers
	workerCtx, workerCancel := context.WithCancel(context.Background())
	mgr.Start(workerCtx)
	go mgr.RunCleanupLoop(workerCtx)

	// Record start time before the geo probe.
	startedAt := time.Now()

	// Geo-probe: classify BBC access (UK ok / geo-block / DNS failure /
	// connectivity failure) so each surfaces distinctly in the API and UI.
	gr := ms.CheckGeo(context.Background())
	geoCheckedAt := time.Now().UTC().Format(time.RFC3339)
	switch gr.Status {
	case bbc.GeoUKOK:
		log.Println("geo-probe: UK access confirmed")
	case bbc.GeoNotUK:
		log.Println("WARNING: geo-blocked -- non-UK exit; BBC iPlayer unavailable")
	case bbc.GeoDNSFailed:
		log.Printf("WARNING: geo-probe DNS failure (%s) -- set VPN_NAMESERVERS to a public resolver (e.g. 1.1.1.1)", gr.Detail)
	default:
		log.Printf("geo-probe: check failed (%s)", gr.Detail)
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
		GeoOK:         gr.Status == bbc.GeoUKOK,
		GeoStatus:     string(gr.Status),
		GeoDetail:     gr.Detail,
		GeoCheckedAt:  geoCheckedAt,
	}
	apiHandler := api.NewHandler(st, hub, mgr, ibl, runtimeStatus)
	apiHandler.RingBuf = ringBuf
	apiHandler.StartedAt = startedAt
	apiHandler.DownloadDir = downloadDir
	apiHandler.GeoProbe = func() bbc.GeoResult {
		return ms.CheckGeo(context.Background())
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
	// Same ordering constraint, same post-construction setter: /api/search
	// reports per-result availability so an API-driven client can skip an
	// episode BBC has not published yet. Issue #52.
	apiHandler.SetProber(prober)
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
		Addr:    resolveListenAddr(),
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
		log.Printf("iplayer-arr listening on %s", srv.Addr)
		// Log at most a short prefix as a configuration-presence
		// breadcrumb rather than a key-recovery hint. The full key is
		// delivered via the 0600 file in the config directory (path
		// logged below) or pinned by the API_KEY environment variable.
		// Logs are served over /api/logs, so leaking the suffix as well
		// would reveal 25% of the secret to anyone who reaches that
		// endpoint. Item 8 / Codex C2 follow-on.
		//
		// apiKeyLogPrefix, not apiKey[:4]: the direct slice panicked on
		// a key shorter than four characters, and it panicked here, in
		// the goroutine that then calls ListenAndServe, so the process
		// died before binding and the container crash-looped.
		if prefix := apiKeyLogPrefix(apiKey); prefix != "" {
			log.Printf("API key configured (prefix=%s...)", prefix)
		} else {
			log.Print("API key configured")
		}
		if keyFileErr == nil {
			log.Printf("API key readable at %s (mode 0600)", keyFilePath)
		}
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

// numCPUDefault returns the NumCPU-aware default for max_workers.
// Caps small hosts at 2 (avoids single-CPU starvation), busy hosts at 4
// (the historical default), with a NumCPU/2 mid-range. Tested for the
// bounds rather than a specific machine's CPU count to keep tests stable
// across the test fleet. v1.5.7: was a fixed constant 4 prior; lowered
// because #42 showed 4 concurrent ffmpeg processes on small hosts cause
// CPU/IO contention that trips the progress watchdog.
func numCPUDefault() int {
	n := runtime.NumCPU() / 2
	if n < 2 {
		return 2
	}
	if n > 4 {
		return 4
	}
	return n
}

func configuredMaxWorkers(st *store.Store) int {
	defaultMaxWorkers := numCPUDefault()

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

// configuredWatchdogTimeout resolves the per-job ffmpeg watchdog timeout
// from (in priority order):
//
//  1. env var IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS (positive integer)
//  2. store config key watchdog_timeout_seconds (positive integer)
//  3. zero, which causes internal/download/ffmpeg.go to use the
//     package default (progressWatchdogTimeout = 60s)
//
// Resolves #42: under heavy concurrency the 60s default can fire
// false positives when ffmpeg is starved of CPU, so users on busy
// hosts need a way to extend the threshold without recompiling.
// configuredAdaptiveThrottle resolves the adaptive stall-throttle config from
// IPLAYER_ARR_ADAPTIVE_THROTTLE_* env vars (priority) then store config keys,
// falling back to the shipped defaults. Set adaptive_throttle_enabled=false
// (or the env to false/0) as the kill-switch. GitHub #50.
func configuredAdaptiveThrottle(st *store.Store) download.AdaptiveThrottleConfig {
	cfg := download.DefaultAdaptiveThrottleConfig()
	cfg.Enabled = envOrStoreBool(st, "IPLAYER_ARR_ADAPTIVE_THROTTLE_ENABLED", "adaptive_throttle_enabled", cfg.Enabled)
	if v, ok := envOrStoreInt(st, "IPLAYER_ARR_ADAPTIVE_THROTTLE_THRESHOLD", "adaptive_throttle_threshold"); ok {
		cfg.Threshold = v
	}
	if v, ok := envOrStoreInt(st, "IPLAYER_ARR_ADAPTIVE_THROTTLE_WINDOW_SECONDS", "adaptive_throttle_window_seconds"); ok {
		cfg.Window = time.Duration(v) * time.Second
	}
	if v, ok := envOrStoreInt(st, "IPLAYER_ARR_ADAPTIVE_THROTTLE_COOLDOWN_SECONDS", "adaptive_throttle_cooldown_seconds"); ok {
		cfg.BaseCooldown = time.Duration(v) * time.Second
	}
	if v, ok := envOrStoreInt(st, "IPLAYER_ARR_ADAPTIVE_THROTTLE_MIN_ACTIVE", "adaptive_throttle_min_active"); ok {
		cfg.Floor = v
	}
	return cfg
}

// envOrStoreInt reads a positive integer from an env var (priority) then a
// store config key. Returns (0, false) when neither holds a valid positive
// value. Mirrors configuredWatchdogTimeout's resolution order.
func envOrStoreInt(st *store.Store, envKey, storeKey string) (int, bool) {
	if raw := os.Getenv(envKey); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v, true
		}
		log.Printf("invalid %s %q, ignoring", envKey, raw)
	}
	if st == nil {
		return 0, false
	}
	raw, _ := st.GetConfig(storeKey)
	if raw == "" {
		return 0, false
	}
	if v, err := strconv.Atoi(raw); err == nil && v > 0 {
		return v, true
	}
	log.Printf("invalid %s %q, ignoring", storeKey, raw)
	return 0, false
}

// envOrStoreBool reads a boolean (true/false/1/0/yes/no/on/off) from an env
// var (priority) then a store config key, falling back to def.
func envOrStoreBool(st *store.Store, envKey, storeKey string, def bool) bool {
	parse := func(raw string) (bool, bool) {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
		return false, false
	}
	if raw := os.Getenv(envKey); raw != "" {
		if v, ok := parse(raw); ok {
			return v
		}
	}
	if st != nil {
		if raw, _ := st.GetConfig(storeKey); raw != "" {
			if v, ok := parse(raw); ok {
				return v
			}
		}
	}
	return def
}

func configuredWatchdogTimeout(st *store.Store) time.Duration {
	if seconds := envIntDefault("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", 0); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if st == nil {
		return 0
	}
	raw, _ := st.GetConfig("watchdog_timeout_seconds")
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 1 {
		log.Printf("invalid watchdog_timeout_seconds %q, using package default", raw)
		return 0
	}
	return time.Duration(seconds) * time.Second
}
