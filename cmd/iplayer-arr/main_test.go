package main

import (
	"path/filepath"
	"testing"

	"github.com/GiteaLN/iplayer-arr/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestConfiguredMaxWorkersDefaultsToTen(t *testing.T) {
	st := testStore(t)

	if got := configuredMaxWorkers(st); got != 10 {
		t.Fatalf("configuredMaxWorkers() = %d, want 10", got)
	}
}

func TestConfiguredMaxWorkersUsesStoredValue(t *testing.T) {
	st := testStore(t)
	if err := st.SetConfig("max_workers", "15"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	if got := configuredMaxWorkers(st); got != 15 {
		t.Fatalf("configuredMaxWorkers() = %d, want 15", got)
	}
}
