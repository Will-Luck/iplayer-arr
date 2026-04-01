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

	"github.com/GiteaLN/iplayer-arr/internal/bbc"
	"github.com/GiteaLN/iplayer-arr/internal/download"
	"github.com/GiteaLN/iplayer-arr/internal/newznab"
	"github.com/GiteaLN/iplayer-arr/internal/sabnzbd"
	"github.com/GiteaLN/iplayer-arr/internal/store"
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
	mgr := download.NewManager(st, downloadDir, 2)

	// http routing
	mux := http.NewServeMux()
	mux.Handle("/newznab/", newznab.NewHandler(ibl, st, ms))
	mux.Handle("/sabnzbd/", sabnzbd.NewHandler(st, mgr))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Printf("iplayer-arr listening on :%s", port)
		log.Printf("API key: %s", apiKey)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

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
