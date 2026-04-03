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
	"syscall"
	"time"

	"github.com/GiteaLN/iplayer-arr/internal/api"
	"github.com/GiteaLN/iplayer-arr/internal/bbc"
	"github.com/GiteaLN/iplayer-arr/internal/download"
	"github.com/GiteaLN/iplayer-arr/internal/newznab"
	"github.com/GiteaLN/iplayer-arr/internal/sabnzbd"
	"github.com/GiteaLN/iplayer-arr/internal/store"
	"github.com/GiteaLN/iplayer-arr/internal/web"
)

func main() {
	configDir := envOr("CONFIG_DIR", "/config")
	downloadDir := envOr("DOWNLOAD_DIR", "/downloads")
	port := envOr("PORT", "8191")

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
	hub := api.NewHub()
	mgr := download.NewManager(st, downloadDir, 10, bbcClient, playlist, ms, hub)

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

	if err := os.MkdirAll(downloadDir, 0755); err != nil {
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
	mux.Handle("/newznab/", newznab.NewHandler(ibl, st, ms))
	mux.Handle("/sabnzbd/", sabnzbd.NewHandler(st, mgr))
	mux.Handle("/api/", apiHandler)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Must be last -- catch-all for SPA routing
	mux.Handle("/", web.SPAHandler())

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Printf("iplayer-arr listening on :%s", port)
		log.Printf("API key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	workerCancel()
	mgr.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("iplayer-arr stopped")
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

// detectLevel returns a log level string based on case-insensitive keyword
// matching in the message.
func detectLevel(msg string) string {
	for i := 0; i < len(msg); i++ {
		switch {
		case i+7 <= len(msg) && equalCI(msg[i:i+7], "WARNING"):
			return "warn"
		case i+5 <= len(msg) && equalCI(msg[i:i+5], "ERROR"):
			return "error"
		case i+5 <= len(msg) && equalCI(msg[i:i+5], "FATAL"):
			return "error"
		case i+5 <= len(msg) && equalCI(msg[i:i+5], "DEBUG"):
			return "debug"
		case i+4 <= len(msg) && equalCI(msg[i:i+4], "WARN"):
			return "warn"
		}
	}
	return "info"
}

// equalCI reports whether a and b are equal under ASCII case-folding.
// Both slices must have the same length.
func equalCI(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 32
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
