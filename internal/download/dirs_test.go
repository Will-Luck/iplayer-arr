package download

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnsureDownloadDir_ModeAtLeast0o775AfterUmask(t *testing.T) {
	// Set umask to 0o002 (the hotio/container default) so 0o775 is not
	// masked down to 0o755. On test hosts with umask 0o022 this would
	// otherwise silently drop the group-write bit and invalidate the
	// assertion.
	oldMask := syscall.Umask(0o002)
	defer syscall.Umask(oldMask)

	tmp := t.TempDir()
	target := filepath.Join(tmp, "downloads")

	if err := EnsureDownloadDir(target); err != nil {
		t.Fatalf("EnsureDownloadDir: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	mode := info.Mode().Perm()
	if mode&0o020 == 0 {
		t.Errorf("expected group-write bit preserved after umask 0o002, got mode %04o", mode)
	}
}

func TestEnsureDownloadDir_AlreadyExists_NoOp(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "downloads")

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir pre-existing: %v", err)
	}

	if err := EnsureDownloadDir(target); err != nil {
		t.Fatalf("first EnsureDownloadDir: %v", err)
	}
	if err := EnsureDownloadDir(target); err != nil {
		t.Fatalf("second EnsureDownloadDir (no-op expected): %v", err)
	}
}

func TestEnsureDownloadDir_NestedPath(t *testing.T) {
	oldMask := syscall.Umask(0o002)
	defer syscall.Umask(oldMask)

	tmp := t.TempDir()
	nested := filepath.Join(tmp, "a", "b", "c", "downloads")

	if err := EnsureDownloadDir(nested); err != nil {
		t.Fatalf("EnsureDownloadDir nested: %v", err)
	}

	for _, p := range []string{
		filepath.Join(tmp, "a"),
		filepath.Join(tmp, "a", "b"),
		filepath.Join(tmp, "a", "b", "c"),
		nested,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
}
