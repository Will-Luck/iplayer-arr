package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAPIKey_GeneratesAndPersists covers a brand new install: no
// API_KEY in the environment and nothing in the store, so a fresh 32-char
// hex key is minted and written back for every later reader.
func TestResolveAPIKey_GeneratesAndPersists(t *testing.T) {
	t.Setenv("API_KEY", "")
	st := testStore(t)

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("generated key length = %d, want 32 (128-bit hex)", len(key))
	}
	stored, _ := st.GetConfig("api_key")
	if stored != key {
		t.Errorf("store holds %q, want the resolved key %q", stored, key)
	}
}

// TestResolveAPIKey_KeepsExistingStoredKey covers a restart: the persisted
// key must survive, otherwise every Sonarr and Radarr instance pointed at
// this box is locked out on each container start.
func TestResolveAPIKey_KeepsExistingStoredKey(t *testing.T) {
	t.Setenv("API_KEY", "")
	st := testStore(t)
	if err := st.SetConfig("api_key", "already-seeded-key"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "already-seeded-key" {
		t.Errorf("key = %q, want the persisted value", key)
	}
}

// TestResolveAPIKey_EnvOverridesStore is the property CI and the smoke
// test depend on: an operator-supplied API_KEY wins over whatever the
// store holds, so a known key can be injected instead of scraped.
func TestResolveAPIKey_EnvOverridesStore(t *testing.T) {
	t.Setenv("API_KEY", "operator-supplied-key")
	st := testStore(t)
	if err := st.SetConfig("api_key", "stale-generated-key"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "operator-supplied-key" {
		t.Errorf("key = %q, want the API_KEY env value", key)
	}
	stored, _ := st.GetConfig("api_key")
	if stored != "operator-supplied-key" {
		t.Errorf("store holds %q, want the env value persisted so newznab and sabnzbd agree", stored)
	}
}

// TestResolveAPIKey_BlankEnvIsIgnored: an empty or whitespace-only
// API_KEY (a common compose-file artefact) must not blank the key and
// lock the operator out.
func TestResolveAPIKey_BlankEnvIsIgnored(t *testing.T) {
	t.Setenv("API_KEY", "   ")
	st := testStore(t)
	if err := st.SetConfig("api_key", "persisted-key"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "persisted-key" {
		t.Errorf("key = %q, want the persisted value (blank env ignored)", key)
	}
}

// TestWriteAPIKeyFile_ModeAndContent pins the out-of-band delivery
// channel that replaces the unauthenticated GET /api/config: the key
// lands in the config directory, owner-readable only.
func TestWriteAPIKeyFile_ModeAndContent(t *testing.T) {
	dir := t.TempDir()

	path, err := writeAPIKeyFile(dir, "abc123")
	if err != nil {
		t.Fatalf("writeAPIKeyFile: %v", err)
	}
	if want := filepath.Join(dir, "api_key"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "abc123\n" {
		t.Errorf("contents = %q, want %q", string(data), "abc123\n")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}
}

// TestWriteAPIKeyFile_TightensExistingMode: os.WriteFile leaves the mode
// of an existing file alone, so an upgrade over a world-readable file
// would silently keep leaking. The write must correct it.
func TestWriteAPIKeyFile_TightensExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api_key")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	if _, err := writeAPIKeyFile(dir, "new-key"); err != nil {
		t.Fatalf("writeAPIKeyFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600 after rewrite over a 0644 file", perm)
	}
}

// TestResolveListenAddr_DefaultsToAllInterfaces preserves the historical
// bind behaviour when BIND_ADDR is unset.
func TestResolveListenAddr_DefaultsToAllInterfaces(t *testing.T) {
	t.Setenv("BIND_ADDR", "")
	t.Setenv("PORT", "")
	if got := resolveListenAddr(); got != ":"+defaultPort {
		t.Errorf("resolveListenAddr() = %q, want %q", got, ":"+defaultPort)
	}
}

// TestResolveListenAddr_HonoursBindAddr lets an operator (and the
// security verification harness) confine the listener to loopback.
func TestResolveListenAddr_HonoursBindAddr(t *testing.T) {
	t.Setenv("BIND_ADDR", "127.0.0.1")
	t.Setenv("PORT", "63999")
	if got := resolveListenAddr(); got != "127.0.0.1:63999" {
		t.Errorf("resolveListenAddr() = %q, want %q", got, "127.0.0.1:63999")
	}
}

// TestResolveAPIKey_ShortEnvKeyIsRefusedNotPanicked is the regression
// test for the crash this branch introduced: main logged a four-character
// prefix with apiKey[:4] and resolveAPIKey accepted any non-blank value,
// so API_KEY=abc panicked with "slice bounds out of range" in the
// goroutine that calls ListenAndServe. The process died before binding
// and the container crash-looped, never serving a request.
//
// The operator must get an actionable error instead.
func TestResolveAPIKey_ShortEnvKeyIsRefusedNotPanicked(t *testing.T) {
	t.Setenv("API_KEY", "abc")
	st := testStore(t)

	key, err := resolveAPIKey(st)
	if err == nil {
		t.Fatalf("resolveAPIKey accepted a 3-character API_KEY and returned %q; want an error", key)
	}
	msg := err.Error()
	for _, want := range []string{"API_KEY", "3 characters", "at least 16"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q; it has to tell the operator what to do", msg, want)
		}
	}
}

// TestResolveAPIKey_ShortEnvKeyDoesNotClobberTheStore: the refusal has to
// happen before the store write. Persisting the short key and then
// failing would leave it behind, so the next start without API_KEY would
// silently adopt a key the previous start rejected.
func TestResolveAPIKey_ShortEnvKeyDoesNotClobberTheStore(t *testing.T) {
	t.Setenv("API_KEY", "abc")
	st := testStore(t)
	if err := st.SetConfig("api_key", "a-perfectly-good-existing-key"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := resolveAPIKey(st); err == nil {
		t.Fatal("expected an error for a 3-character API_KEY")
	}

	stored, _ := st.GetConfig("api_key")
	if stored != "a-perfectly-good-existing-key" {
		t.Errorf("store holds %q; the rejected short key must not have been written", stored)
	}
}

// TestResolveAPIKey_MinimumLengthEnvKeyAccepted pins the boundary so the
// guard cannot drift into rejecting a legitimate operator key.
func TestResolveAPIKey_MinimumLengthEnvKeyAccepted(t *testing.T) {
	exact := strings.Repeat("k", minOperatorAPIKeyLen)
	t.Setenv("API_KEY", exact)
	st := testStore(t)

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("a key of exactly the minimum length was refused: %v", err)
	}
	if key != exact {
		t.Errorf("key = %q, want %q", key, exact)
	}
}

// TestResolveAPIKey_ShortStoredKeyIsKept: a key already in the store is
// the operator's working credential. Refusing it would lock them out of
// the dashboard over a value they can only change through that dashboard.
func TestResolveAPIKey_ShortStoredKeyIsKept(t *testing.T) {
	t.Setenv("API_KEY", "")
	st := testStore(t)
	if err := st.SetConfig("api_key", "abc"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	key, err := resolveAPIKey(st)
	if err != nil {
		t.Fatalf("a short stored key must not be fatal: %v", err)
	}
	if key != "abc" {
		t.Errorf("key = %q, want the stored value", key)
	}
}

// TestAPIKeyLogPrefix_IsLengthSafe covers the other half of the crash.
// A short key can still reach the log from the store, written by an
// older build, so the log helper has to be safe on its own rather than
// relying on the API_KEY guard above.
func TestAPIKeyLogPrefix_IsLengthSafe(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"", ""},
		{"a", ""},
		{"abc", ""},
		{"abcdefghijk", ""}, // 11, one under the reveal threshold
		{"abcdefghijkl", "abcd"},
		{"9306d287598bdb0561ec0c138312f5ad", "9306"},
	}
	for _, tc := range cases {
		got := apiKeyLogPrefix(tc.key)
		if got != tc.want {
			t.Errorf("apiKeyLogPrefix(%q) = %q, want %q", tc.key, got, tc.want)
		}
		if got != "" && len(got) >= len(tc.key) {
			t.Errorf("apiKeyLogPrefix(%q) returned the whole key", tc.key)
		}
	}
}

// TestWriteAPIKeyFile_DoesNotFollowASymlink: without O_EXCL on a fresh
// name, a symlink planted at the target gave an attacker truncate plus
// chmod on any file the container uid could reach.
func TestWriteAPIKeyFile_DoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch me\n"), 0o644); err != nil {
		t.Fatalf("create victim: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "api_key")); err != nil {
		t.Skipf("symlinks unavailable on this filesystem: %v", err)
	}

	if _, err := writeAPIKeyFile(dir, "a-long-enough-secret-key"); err != nil {
		t.Fatalf("writeAPIKeyFile: %v", err)
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(got) != "do not touch me\n" {
		t.Errorf("the symlink was followed: victim now holds %q", string(got))
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatalf("stat victim: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("victim mode changed to %04o; the chmod followed the symlink", perm)
	}

	// The real file must be a regular file holding the key, not a link.
	target := filepath.Join(dir, "api_key")
	li, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		t.Error("target is still a symlink; it should have been replaced by a regular file")
	}
	body, _ := os.ReadFile(target)
	if string(body) != "a-long-enough-secret-key\n" {
		t.Errorf("target holds %q, want the key", string(body))
	}
}

// TestWriteAPIKeyFile_FailureLeavesTargetUntouched pins the ordering.
// The old write-then-chmod put the plaintext key on disk first and
// tightened the mode second, and on a mount that refuses chmod it
// returned an error after the key had landed, so main's warning claimed
// nothing was written when something was.
func TestWriteAPIKeyFile_FailureLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "api_key")
	if err := os.WriteFile(target, []byte("previous-key\n"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	// Deny creation of the temp file by making the directory read-only.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only here: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := writeAPIKeyFile(dir, "a-long-enough-secret-key"); err == nil {
		t.Skip("this filesystem still permits the write (running as root?)")
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(body) != "previous-key\n" {
		t.Errorf("target was modified on a failed write: %q", string(body))
	}
}

// TestWriteAPIKeyFile_LeavesNoTempFileBehind: the staging file holds the
// key in plaintext, so it must not survive the call.
func TestWriteAPIKeyFile_LeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeAPIKeyFile(dir, "a-long-enough-secret-key"); err != nil {
		t.Fatalf("writeAPIKeyFile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "api_key" {
			t.Errorf("stray file left in the config directory: %q", e.Name())
		}
	}
}

// TestWriteAPIKeyFile_FailedRenameLeavesNoPlaintextBehind exercises the
// failure path that matters most: the temp file has already been created
// and the key already written to it when the rename fails. If the
// deferred cleanup were dropped, a world-readable-by-nothing but still
// present file holding the plaintext key would accumulate in the config
// directory on every restart, which is precisely the leak the atomic
// rewrite exists to prevent.
//
// A directory at the target path makes os.Rename fail deterministically
// without needing a hostile filesystem.
func TestWriteAPIKeyFile_FailedRenameLeavesNoPlaintextBehind(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "api_key"), 0o700); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}

	if _, err := writeAPIKeyFile(dir, "a-long-enough-secret-key"); err == nil {
		t.Fatal("expected the rename onto a directory to fail")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "api_key" {
			continue // the blocking directory we created
		}
		body, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		t.Errorf("failed write left %q behind holding %q", e.Name(), string(body))
	}
}
