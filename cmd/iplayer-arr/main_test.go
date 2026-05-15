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

func TestConfiguredMaxWorkersDefault(t *testing.T) {
	st := testStore(t)

	if got := configuredMaxWorkers(st); got != 4 {
		t.Fatalf("configuredMaxWorkers() = %d, want 4", got)
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

func TestMigrateQualityConfig(t *testing.T) {
	cases := []struct {
		name      string
		initial   string
		wantValue string
		wantMoved bool
	}{
		{"empty leaves empty", "", "", false},
		{"any kept", "any", "any", false},
		{"1080p kept", "1080p", "1080p", false},
		{"720p kept", "720p", "720p", false},
		{"540p kept", "540p", "540p", false},
		{"396p kept", "396p", "396p", false},
		{"mixed case 720P kept", "720P", "720P", false},
		{"with whitespace kept", " 720p ", " 720p ", false},
		{"legacy Default normalised", "Default", "any", true},
		{"legacy 480p normalised", "480p", "any", true},
		{"legacy best normalised", "best", "any", true},
		{"garbage normalised", "xyzzy", "any", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testStore(t)
			if tc.initial != "" {
				if err := st.SetConfig("quality", tc.initial); err != nil {
					t.Fatalf("seed SetConfig: %v", err)
				}
			}
			moved := migrateQualityConfig(st)
			if moved != tc.wantMoved {
				t.Errorf("migrateQualityConfig moved=%v, want %v", moved, tc.wantMoved)
			}
			got, _ := st.GetConfig("quality")
			if got != tc.wantValue {
				t.Errorf("quality after migrate = %q, want %q", got, tc.wantValue)
			}
		})
	}
}

func TestMigrateQualityConfig_NilStore(t *testing.T) {
	if migrateQualityConfig(nil) {
		t.Error("migrateQualityConfig(nil) returned true, want false")
	}
}

func TestMigrateQualityConfig_Idempotent(t *testing.T) {
	st := testStore(t)
	st.SetConfig("quality", "Default")
	if !migrateQualityConfig(st) {
		t.Fatal("first migrate did not move legacy value")
	}
	if migrateQualityConfig(st) {
		t.Error("second migrate moved a value that should already be normalised")
	}
	got, _ := st.GetConfig("quality")
	if got != "any" {
		t.Errorf("quality after double migrate = %q, want any", got)
	}
}
