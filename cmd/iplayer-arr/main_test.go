package main

import (
	"path/filepath"
	"testing"
	"time"

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

// TestConfiguredMaxWorkersDefault: the default now comes from
// numCPUDefault() rather than a fixed constant. v1.5.7 change driven
// by #42 (4 workers on small hosts cause CPU/IO contention that
// trips the ffmpeg progress watchdog).
func TestConfiguredMaxWorkersDefault(t *testing.T) {
	st := testStore(t)
	want := numCPUDefault()
	if got := configuredMaxWorkers(st); got != want {
		t.Fatalf("configuredMaxWorkers() = %d, want %d (numCPUDefault)", got, want)
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

// TestNumCPUDefault_BoundsCheck locks the contract: numCPUDefault is
// always in [2, 4] regardless of host CPU count. Tested against the
// bounds rather than a specific value to stay stable across the CI
// fleet (2-core runners, 8-core dev boxes, 32-core servers).
func TestNumCPUDefault_BoundsCheck(t *testing.T) {
	got := numCPUDefault()
	if got < 2 || got > 4 {
		t.Errorf("numCPUDefault() = %d, want in [2, 4]", got)
	}
}

// TestConfiguredWatchdogTimeoutDefault: no env, no store entry,
// returns zero (which causes ffmpeg.go to use progressWatchdogTimeout).
func TestConfiguredWatchdogTimeoutDefault(t *testing.T) {
	st := testStore(t)
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "")

	if got := configuredWatchdogTimeout(st); got != 0 {
		t.Errorf("configuredWatchdogTimeout(empty) = %v, want 0", got)
	}
}

// TestConfiguredWatchdogTimeoutNilStore: tolerates a nil store
// (early-boot code paths) and returns zero so the package default
// kicks in.
func TestConfiguredWatchdogTimeoutNilStore(t *testing.T) {
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "")

	if got := configuredWatchdogTimeout(nil); got != 0 {
		t.Errorf("configuredWatchdogTimeout(nil) = %v, want 0", got)
	}
}

// TestConfiguredWatchdogTimeoutEnvOverride: env var wins when set
// to a positive integer.
func TestConfiguredWatchdogTimeoutEnvOverride(t *testing.T) {
	st := testStore(t)
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "180")

	got := configuredWatchdogTimeout(st)
	if got != 180*time.Second {
		t.Errorf("configuredWatchdogTimeout(env=180) = %v, want 180s", got)
	}
}

// TestConfiguredWatchdogTimeoutStoreOverride: store config wins when
// set and env is unset.
func TestConfiguredWatchdogTimeoutStoreOverride(t *testing.T) {
	st := testStore(t)
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "")
	if err := st.SetConfig("watchdog_timeout_seconds", "120"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	got := configuredWatchdogTimeout(st)
	if got != 120*time.Second {
		t.Errorf("configuredWatchdogTimeout(store=120) = %v, want 120s", got)
	}
}

// TestConfiguredWatchdogTimeoutEnvBeatsStore: env wins over store
// when both are set.
func TestConfiguredWatchdogTimeoutEnvBeatsStore(t *testing.T) {
	st := testStore(t)
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "200")
	if err := st.SetConfig("watchdog_timeout_seconds", "120"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	got := configuredWatchdogTimeout(st)
	if got != 200*time.Second {
		t.Errorf("configuredWatchdogTimeout(env=200, store=120) = %v, want 200s (env wins)", got)
	}
}

// TestConfiguredWatchdogTimeoutInvalid: an invalid store value
// (non-numeric or non-positive) falls back to zero so the package
// default applies, with an info-level log line for diagnostic
// visibility.
func TestConfiguredWatchdogTimeoutInvalid(t *testing.T) {
	st := testStore(t)
	t.Setenv("IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS", "")

	for _, raw := range []string{"banana", "0", "-5", "1.5"} {
		if err := st.SetConfig("watchdog_timeout_seconds", raw); err != nil {
			t.Fatalf("SetConfig %q: %v", raw, err)
		}
		if got := configuredWatchdogTimeout(st); got != 0 {
			t.Errorf("configuredWatchdogTimeout(store=%q) = %v, want 0 (invalid -> default)", raw, got)
		}
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

func TestWaitWithTimeout_Completes(t *testing.T) {
	done := false
	ok := waitWithTimeout(func() {
		time.Sleep(50 * time.Millisecond)
		done = true
	}, 500*time.Millisecond)
	if !ok {
		t.Error("waitWithTimeout returned false for a fn that completes in time")
	}
	if !done {
		t.Error("fn did not run before waitWithTimeout returned")
	}
}

func TestWaitWithTimeout_Exceeds(t *testing.T) {
	ok := waitWithTimeout(func() {
		time.Sleep(500 * time.Millisecond)
	}, 100*time.Millisecond)
	if ok {
		t.Error("waitWithTimeout returned true for a fn that exceeded its deadline")
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
