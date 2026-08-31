package bbc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
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
