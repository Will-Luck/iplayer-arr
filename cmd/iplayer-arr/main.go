package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
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

	// Geo-probe: check if BBC content is accessible
	geoOK := false
	bbcStatus, geoErr := bbcClient.Head("https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_hd/format/xml")
	if geoErr != nil {
		log.Printf("WARNING: geo-probe failed: %v", geoErr)
	} else if bbcStatus == 200 {
		geoOK = true
		log.Println("geo-probe: UK access confirmed")
	} else if bbcStatus == 403 {
		log.Println("WARNING: geo-blocked -- BBC iPlayer content unavailable without a UK connection")
	} else {
		log.Printf("geo-probe: unexpected status %d", bbcStatus)
	}

	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		log.Printf("WARNING: cannot create download dir %s: %v", downloadDir, err)
	}

	// http routing
	runtimeStatus := &api.RuntimeStatus{
		FFmpegVersion: ffVer,
		GeoOK:         geoOK,
	}
	apiHandler := api.NewHandler(st, hub, mgr, ibl, runtimeStatus)

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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
