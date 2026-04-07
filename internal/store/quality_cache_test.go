package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		os.RemoveAll(dir)
	})
	return s
}

func TestQualityCache_PutGetRoundtrip(t *testing.T) {
	s := newTestStore(t)
	original := &QualityCache{
		PID:      "b0abcdef",
		ShowName: "eastenders",
		Heights:  []int{720, 540},
		ProbedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := s.PutQualityCache(original); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.GetQualityCache("b0abcdef")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cache hit, got nil")
	}
	if got.PID != original.PID || got.ShowName != original.ShowName {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, original)
	}
	if len(got.Heights) != len(original.Heights) || got.Heights[0] != 720 {
		t.Errorf("Heights round-trip mismatch: got %v", got.Heights)
	}
}

func TestQualityCache_GetMiss_NilNilNoError(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetQualityCache("b0notthere")
	if err != nil {
		t.Fatalf("get miss should not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil on miss, got %+v", got)
	}
}

func TestQualityCache_Delete(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutQualityCache(&QualityCache{PID: "pid1", ShowName: "show", Heights: []int{720}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.DeleteQualityCache("pid1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.GetQualityCache("pid1")
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestQualityCache_DeleteByShow(t *testing.T) {
	s := newTestStore(t)
	entries := []*QualityCache{
		{PID: "p1", ShowName: "eastenders", Heights: []int{720, 540}},
		{PID: "p2", ShowName: "eastenders", Heights: []int{720, 540}},
		{PID: "p3", ShowName: "doctor who", Heights: []int{1080, 720}},
	}
	for _, e := range entries {
		if err := s.PutQualityCache(e); err != nil {
			t.Fatalf("put %s: %v", e.PID, err)
		}
	}
	if err := s.DeleteQualityCacheByShow("eastenders"); err != nil {
		t.Fatalf("delete by show: %v", err)
	}
	// p1 and p2 should be gone; p3 should remain.
	for _, pid := range []string{"p1", "p2"} {
		got, _ := s.GetQualityCache(pid)
		if got != nil {
			t.Errorf("%s should have been deleted, got %+v", pid, got)
		}
	}
	got, _ := s.GetQualityCache("p3")
	if got == nil {
		t.Error("p3 (doctor who) should still exist")
	}
}

func TestQualityCache_PutNormalisesShowName(t *testing.T) {
	s := newTestStore(t)
	// Caller passes mixed-case, whitespace-padded name.
	qc := &QualityCache{PID: "px", ShowName: "  Doctor Who  ", Heights: []int{1080, 720}}
	if err := s.PutQualityCache(qc); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.GetQualityCache("px")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cache hit")
	}
	if got.ShowName != "doctor who" {
		t.Errorf("expected normalised show name 'doctor who', got %q", got.ShowName)
	}
}

func TestQualityCache_DeleteByShow_CaseInsensitive(t *testing.T) {
	// For each differently-cased input, populate fresh entries then
	// delete and assert they're gone. Using the same normalisation
	// at write time (from PutNormalisesShowName) means the bucket
	// contents are always "doctor who" regardless of input casing.
	cases := []string{"Doctor Who", "DOCTOR WHO", " doctor who ", "Doctor Who"}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			s := newTestStore(t)
			qc := &QualityCache{PID: "px", ShowName: "Doctor Who", Heights: []int{1080, 720}}
			if err := s.PutQualityCache(qc); err != nil {
				t.Fatalf("put: %v", err)
			}
			if err := s.DeleteQualityCacheByShow(input); err != nil {
				t.Fatalf("delete by show %q: %v", input, err)
			}
			got, _ := s.GetQualityCache("px")
			if got != nil {
				t.Errorf("expected px to be deleted via input %q, still exists", input)
			}
		})
	}
}
