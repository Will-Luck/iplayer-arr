package api

import (
	"testing"
)

func TestResolveDownloadDir_EnvWins(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/data"
	if got := h.ResolveDownloadDir(); got != "/data" {
		t.Errorf("ResolveDownloadDir() = %q, want /data", got)
	}
}

func TestResolveDownloadDir_StoreFallback(t *testing.T) {
	h, st := testAPI(t)
	st.SetConfig("download_dir", "/stored")
	h.DownloadDir = ""
	if got := h.ResolveDownloadDir(); got != "/stored" {
		t.Errorf("ResolveDownloadDir() = %q, want /stored", got)
	}
}

func TestResolveDownloadDir_DefaultFallback(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = ""
	if got := h.ResolveDownloadDir(); got != "/downloads" {
		t.Errorf("ResolveDownloadDir() = %q, want /downloads", got)
	}
}
