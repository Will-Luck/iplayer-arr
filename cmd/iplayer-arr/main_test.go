package main

import (
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
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

func TestResolvePort_DefaultWhenUnset(t *testing.T) {
	t.Setenv("PORT", "")
	if got := resolvePort(); got != defaultPort {
		t.Errorf("resolvePort() with PORT='' = %q, want %q", got, defaultPort)
	}
	if defaultPort != "62001" {
		t.Errorf("defaultPort = %q, want 62001 (FlareSolverr collision fix)", defaultPort)
	}
}

func TestResolvePort_EnvOverride(t *testing.T) {
	t.Setenv("PORT", "9999")
	if got := resolvePort(); got != "9999" {
		t.Errorf("resolvePort() with PORT=9999 = %q, want 9999", got)
	}
}
