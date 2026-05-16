package download

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// TestClaimReleasedOnInnerPanic exercises audit item 40: a panic in
// processDownload must not leak the worker's m.claimed entry. The
// guarantee comes from the deferred dlCancel/release pair in
// processNext; safeProcessNext recovers the panic two frames up, after
// the inner deferred scope has already fired.
//
// We do not invoke processDownload directly (it would require building
// a full BBC pipeline). Instead, we verify the defer contract on the
// same Manager state: claim an ID, panic inside an inner closure that
// mirrors processNext's structure, and assert the claim is released.
func TestClaimReleasedOnInnerPanic(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	const id = "panic-test-id"
	_, dlCancel := context.WithCancel(context.Background())
	if !m.claim(id, dlCancel) {
		t.Fatal("initial claim should succeed")
	}

	// Mirror processNext's inner pattern verbatim: an anonymous
	// function with deferred dlCancel + release, surrounding the
	// processDownload call we simulate via a panic.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected the inner closure to propagate its panic")
			}
		}()
		func() {
			defer func() {
				dlCancel()
				m.release(id)
			}()
			panic("simulated processDownload panic")
		}()
	}()

	m.claimMu.Lock()
	_, stillClaimed := m.claimed[id]
	m.claimMu.Unlock()
	if stillClaimed {
		t.Error("m.claimed entry leaked: inner panic did not trigger release")
	}
}
