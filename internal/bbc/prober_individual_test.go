package bbc

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// perPIDPlaylistResolver fails only the PIDs named in errByPID. The
// shared fakePlaylistResolver carries a single error for every PID,
// which cannot express "one episode of this show is unpublished".
type perPIDPlaylistResolver struct {
	errByPID map[string]error
	calls    int32
}

func (f *perPIDPlaylistResolver) ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error) {
	atomic.AddInt32(&f.calls, 1)
	if err, ok := f.errByPID[pid]; ok {
		return nil, err
	}
	return &PlaylistInfo{VPID: "vpid-" + pid}, nil
}

// motdGroup is the issue #52 shape: several published episodes of one
// show plus the newest one, whose playlist BBC has not published yet.
func motdGroup() []ProbeItem {
	return []ProbeItem{
		{PID: "m00pub01", ShowName: "match of the day"},
		{PID: "m00pub02", ShowName: "match of the day"},
		{PID: "m00new03", ShowName: "match of the day"},
	}
}

// TestPrefetchPIDs_GroupLeaderMasksSiblingNotYetAvailable documents why
// the grouped prefetch cannot answer "is THIS episode published yet".
// probeShowGroup elects group[0] as the leader and, when it resolves,
// returns that verdict for every sibling. The unpublished episode comes
// back unflagged. This is correct for the newznab feed, where a whole
// brand shares one quality set, and wrong for a per-PID availability
// answer. Pinned so the limitation cannot be silently "fixed" into the
// feed path, and so the reason PrefetchPIDsIndividually exists is
// executable rather than a comment.
func TestPrefetchPIDs_GroupLeaderMasksSiblingNotYetAvailable(t *testing.T) {
	pl := &perPIDPlaylistResolver{errByPID: map[string]error{"m00new03": ErrNotYetAvailable}}
	p := NewQualityProber(pl, &fakeMediaSelector{}, &fakeFHDProber{}, newFakeCacheStore(), 4, time.Second)

	out := p.PrefetchPIDs(context.Background(), motdGroup())

	if out.NotYetAvailable["m00new03"] {
		t.Fatal("grouped prefetch unexpectedly flagged the sibling; the leader-election limitation this test documents is gone, revisit PrefetchPIDsIndividually")
	}
}

// TestPrefetchPIDsIndividually_FlagsUnpublishedSibling is the /api/search
// requirement from issue #52: every PID gets its own verdict, so the
// unpublished episode of an otherwise-available show is reported as
// not-yet-available instead of inheriting its siblings' answer.
func TestPrefetchPIDsIndividually_FlagsUnpublishedSibling(t *testing.T) {
	pl := &perPIDPlaylistResolver{errByPID: map[string]error{"m00new03": ErrNotYetAvailable}}
	cache := newFakeCacheStore()
	p := NewQualityProber(pl, &fakeMediaSelector{}, &fakeFHDProber{}, cache, 4, time.Second)

	out := p.PrefetchPIDsIndividually(context.Background(), motdGroup())

	if !out.NotYetAvailable["m00new03"] {
		t.Error("unpublished episode not flagged not-yet-available")
	}
	for _, pid := range []string{"m00pub01", "m00pub02"} {
		if out.NotYetAvailable[pid] {
			t.Errorf("published episode %s wrongly flagged not-yet-available", pid)
		}
		if len(out.Heights[pid]) == 0 {
			t.Errorf("published episode %s has no heights, got %v", pid, out.Heights[pid])
		}
	}
	if _, cached := cache.data["m00new03"]; cached {
		t.Error("not-yet-available PID must never be cached")
	}
	// The cache rows written for the published episodes must keep the
	// real show name; DeleteQualityCacheByShow reads that field.
	if got := cache.data["m00pub01"]; got == nil || got.ShowName != "match of the day" {
		t.Errorf("cache row for m00pub01 = %+v, want ShowName %q", got, "match of the day")
	}
}

// TestPrefetchPIDsIndividually_DedupesPIDs keeps a repeated PID from
// costing a second probe.
func TestPrefetchPIDsIndividually_DedupesPIDs(t *testing.T) {
	pl := &perPIDPlaylistResolver{}
	p := NewQualityProber(pl, &fakeMediaSelector{}, &fakeFHDProber{}, newFakeCacheStore(), 4, time.Second)

	items := []ProbeItem{
		{PID: "m00dup01", ShowName: "show"},
		{PID: "m00dup01", ShowName: "show"},
	}
	out := p.PrefetchPIDsIndividually(context.Background(), items)

	if got := atomic.LoadInt32(&pl.calls); got != 1 {
		t.Errorf("playlist calls = %d, want 1 (duplicate PID probed twice)", got)
	}
	if len(out.Heights["m00dup01"]) == 0 {
		t.Errorf("heights missing for the deduped PID: %v", out.Heights)
	}
}

// TestPrefetchPIDsIndividually_EmptyInput returns usable, non-nil maps.
func TestPrefetchPIDsIndividually_EmptyInput(t *testing.T) {
	p := NewQualityProber(&perPIDPlaylistResolver{}, &fakeMediaSelector{}, &fakeFHDProber{}, newFakeCacheStore(), 4, time.Second)

	out := p.PrefetchPIDsIndividually(context.Background(), nil)

	if out.Heights == nil || out.NotYetAvailable == nil {
		t.Fatalf("expected non-nil maps, got %+v", out)
	}
	if len(out.Heights) != 0 || len(out.NotYetAvailable) != 0 {
		t.Errorf("expected empty maps, got %+v", out)
	}
}

// newRealCacheStore opens a real BoltDB store, so cache rows go through
// the JSON encoding production uses.
func newRealCacheStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Gitea #75 acceptance: the grouped feed probe writes cache rows for the
// siblings it never probed. Those rows must not answer the individual
// availability probe, or an unpublished episode of a show the feed has
// already seen reads as available.
func TestPrefetchPIDsIndividually_PropagatedSiblingRowStillNotYetAvailable(t *testing.T) {
	st := newRealCacheStore(t)
	pl := &perPIDPlaylistResolver{errByPID: map[string]error{"m00new03": ErrNotYetAvailable}}
	p := NewQualityProber(pl, &fakeMediaSelector{}, &fakeFHDProber{}, st, 4, time.Second)

	// The newznab feed path: m00pub01 leads, so m00pub02 and m00new03
	// get rows without being probed.
	feed := p.PrefetchPIDs(context.Background(), motdGroup())
	if len(feed.Heights["m00new03"]) == 0 {
		t.Fatalf("precondition: the grouped probe hands the show's heights to every sibling, got %v", feed.Heights)
	}
	if row, err := st.GetQualityCache("m00new03"); err != nil || row == nil {
		t.Fatalf("precondition: the grouped probe writes a row for the unprobed sibling, got %+v, %v", row, err)
	}

	out := p.PrefetchPIDsIndividually(context.Background(), motdGroup())

	if !out.NotYetAvailable["m00new03"] {
		t.Errorf("unpublished m00new03 not flagged not-yet-available (heights %v): the propagated row answered for it",
			out.Heights["m00new03"])
	}
	if len(out.Heights["m00new03"]) != 0 {
		t.Errorf("unpublished m00new03 heights = %v, want none", out.Heights["m00new03"])
	}
	for _, pid := range []string{"m00pub01", "m00pub02"} {
		if out.NotYetAvailable[pid] || len(out.Heights[pid]) == 0 {
			t.Errorf("published %s: nya = %v, heights = %v; want available", pid, out.NotYetAvailable[pid], out.Heights[pid])
		}
	}
}

// Each row says where it came from, and a probe of a sibling replaces its
// propagated row with a probed one.
func TestPrefetchPIDs_SiblingRowsAreMarkedPropagated(t *testing.T) {
	st := newRealCacheStore(t)
	p := NewQualityProber(&perPIDPlaylistResolver{}, &fakeMediaSelector{}, &fakeFHDProber{}, st, 4, time.Second)

	_ = p.PrefetchPIDs(context.Background(), motdGroup())
	for pid, wantPropagated := range map[string]bool{"m00pub01": false, "m00pub02": true, "m00new03": true} {
		row, err := st.GetQualityCache(pid)
		if err != nil || row == nil {
			t.Fatalf("GetQualityCache(%s) = %+v, %v", pid, row, err)
		}
		if row.Propagated != wantPropagated {
			t.Errorf("%s Propagated = %v, want %v", pid, row.Propagated, wantPropagated)
		}
	}

	_ = p.PrefetchPIDsIndividually(context.Background(), []ProbeItem{{PID: "m00pub02", ShowName: "match of the day"}})
	if row, _ := st.GetQualityCache("m00pub02"); row == nil || row.Propagated {
		t.Errorf("after its own probe m00pub02 row = %+v, want a probed row", row)
	}
}

// The feed path still reuses a propagated row for the whole group with no
// BBC calls: the flag only changes the per-PID availability answer.
func TestPrefetchPIDs_PropagatedRowStillServesTheFeed(t *testing.T) {
	st := newRealCacheStore(t)
	if err := st.PutQualityCache(&store.QualityCache{
		PID: "m00pub02", ShowName: "match of the day", Heights: []int{720, 540}, ProbedAt: time.Now(), Propagated: true,
	}); err != nil {
		t.Fatalf("PutQualityCache: %v", err)
	}
	pl := &perPIDPlaylistResolver{}
	ms := &fakeMediaSelector{}
	p := NewQualityProber(pl, ms, &fakeFHDProber{}, st, 4, time.Second)

	out := p.PrefetchPIDs(context.Background(), motdGroup())

	if got, gotMS := atomic.LoadInt32(&pl.calls), atomic.LoadInt32(&ms.calls); got != 0 || gotMS != 0 {
		t.Errorf("BBC calls: playlist %d, mediaselector %d; want 0 (the propagated row covers the group)", got, gotMS)
	}
	for _, item := range motdGroup() {
		if h := out.Heights[item.PID]; len(h) != 2 || h[0] != 720 || h[1] != 540 {
			t.Errorf("%s heights = %v, want [720 540]", item.PID, h)
		}
	}
}

// Review focus: a propagated write must not overwrite a sibling's own
// probed row (here with different heights).
func TestPrefetchPIDs_PropagationKeepsRealSiblingRow(t *testing.T) {
	st := newRealCacheStore(t)
	now := time.Now()
	for _, qc := range []*store.QualityCache{
		{PID: "m00pub01", ShowName: "match of the day", Heights: []int{720}, ProbedAt: now, Propagated: true},
		{PID: "m00pub02", ShowName: "match of the day", Heights: []int{1080, 720}, ProbedAt: now},
	} {
		if err := st.PutQualityCache(qc); err != nil {
			t.Fatalf("PutQualityCache(%s): %v", qc.PID, err)
		}
	}
	p := NewQualityProber(&perPIDPlaylistResolver{}, &fakeMediaSelector{}, &fakeFHDProber{}, st, 4, time.Second)

	_ = p.PrefetchPIDs(context.Background(), motdGroup())

	row, _ := st.GetQualityCache("m00pub02")
	if row == nil || row.Propagated || len(row.Heights) != 2 || row.Heights[0] != 1080 {
		t.Errorf("m00pub02 row = %+v, want its own probed [1080 720] row kept", row)
	}
	if row, _ := st.GetQualityCache("m00new03"); row == nil || !row.Propagated {
		t.Errorf("m00new03 row = %+v, want a propagated row", row)
	}
}

// Review focus: a propagated row whose PID then fails transiently reads
// unknown (no heights, no NYA flag), never available, and the row stays
// for the feed.
func TestPrefetchPIDsIndividually_PropagatedRowWithTransientFailureIsUnknown(t *testing.T) {
	st := newRealCacheStore(t)
	if err := st.PutQualityCache(&store.QualityCache{
		PID: "m00new03", ShowName: "match of the day", Heights: []int{720, 540}, ProbedAt: time.Now(), Propagated: true,
	}); err != nil {
		t.Fatalf("PutQualityCache: %v", err)
	}
	pl := &perPIDPlaylistResolver{errByPID: map[string]error{"m00new03": errors.New("fetch episode: HTTP 503")}}
	p := NewQualityProber(pl, &fakeMediaSelector{}, &fakeFHDProber{}, st, 4, time.Second)

	out := p.PrefetchPIDsIndividually(context.Background(), []ProbeItem{{PID: "m00new03", ShowName: "match of the day"}})

	if out.Heights["m00new03"] != nil || out.NotYetAvailable["m00new03"] {
		t.Errorf("heights = %v, nya = %v; want unknown (nil, false)", out.Heights["m00new03"], out.NotYetAvailable["m00new03"])
	}
	if row, _ := st.GetQualityCache("m00new03"); row == nil || !row.Propagated {
		t.Errorf("row = %+v, want the propagated row left in place for the feed", row)
	}
}
