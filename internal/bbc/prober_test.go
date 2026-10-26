package bbc

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// --- fakes ---

type fakePlaylistResolver struct {
	byPID map[string]*PlaylistInfo
	err   error
	calls int32
}

func (f *fakePlaylistResolver) ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	if info, ok := f.byPID[pid]; ok {
		return info, nil
	}
	return &PlaylistInfo{VPID: "vpid-" + pid}, nil
}

type fakeMediaSelector struct {
	byVPID map[string]*StreamSet
	err    error
	calls  int32
	delay  time.Duration
}

func (f *fakeMediaSelector) ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if ss, ok := f.byVPID[vpid]; ok {
		return ss, nil
	}
	return &StreamSet{Video: []VideoStream{{Height: 720, Bitrate: 1000, Format: "hls", URL: "https://example.com/master.m3u8"}, {Height: 540, Bitrate: 500, Format: "hls", URL: "https://example.com/master.m3u8"}}}, nil
}

type fakeFHDProber struct {
	found bool
	err   error
	calls int32

	// VerifyFHD answer, used when mediaselector already claims 1080
	// (GitHub #54). Zero values mean "no 1080, master lists nothing",
	// so tests exercising a 1080 claim must set these explicitly.
	verifyHas1080   bool
	verifyMaxListed int
	verifyErr       error
	verifyCalls     int32
}

func (f *fakeFHDProber) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (string, bool, error) {
	atomic.AddInt32(&f.calls, 1)
	return "https://example.com/stream-video=12000000.m3u8", f.found, f.err
}

func (f *fakeFHDProber) VerifyFHD(ctx context.Context, hlsMasterURL string) (bool, int, error) {
	atomic.AddInt32(&f.verifyCalls, 1)
	return f.verifyHas1080, f.verifyMaxListed, f.verifyErr
}

type fakeCacheStore struct {
	mu     sync.Mutex
	data   map[string]*store.QualityCache
	writes int // PutQualityCache call count (Issue #44: assert not-yet-available is never cached)
}

func newFakeCacheStore() *fakeCacheStore {
	return &fakeCacheStore{data: make(map[string]*store.QualityCache)}
}

func (f *fakeCacheStore) GetQualityCache(pid string) (*store.QualityCache, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[pid], nil
}

func (f *fakeCacheStore) PutQualityCache(qc *store.QualityCache) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	f.data[qc.PID] = qc
	return nil
}

// --- tests ---

func TestPrefetch_CacheHit_NoHTTP(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()
	st.PutQualityCache(&store.QualityCache{PID: "p1", Heights: []int{720, 540}})

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if pl.calls != 0 || ms.calls != 0 || fhd.calls != 0 {
		t.Errorf("cache hit should skip HTTP; calls: pl=%d ms=%d fhd=%d", pl.calls, ms.calls, fhd.calls)
	}
	if len(out.Heights["p1"]) != 2 || out.Heights["p1"][0] != 720 {
		t.Errorf("expected cached heights [720,540], got %v", out.Heights["p1"])
	}
}

func TestPrefetch_CacheMiss_PopulatesAndPersists(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	_ = p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if st.data["p1"] == nil {
		t.Fatal("expected cache entry after probe")
	}
	if pl.calls != 1 || ms.calls != 1 {
		t.Errorf("expected one call each; got pl=%d ms=%d", pl.calls, ms.calls)
	}

	// Second call should hit the cache.
	pl.calls, ms.calls, fhd.calls = 0, 0, 0
	_ = p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})
	if pl.calls != 0 || ms.calls != 0 || fhd.calls != 0 {
		t.Errorf("second probe should be cache hit; calls: pl=%d ms=%d fhd=%d", pl.calls, ms.calls, fhd.calls)
	}
}

func TestPrefetch_PlaylistError_ReturnsNilNoCacheWrite(t *testing.T) {
	pl := &fakePlaylistResolver{err: errors.New("playlist down")}
	ms := &fakeMediaSelector{}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if out.Heights["p1"] != nil {
		t.Errorf("expected nil result on playlist error, got %v", out.Heights["p1"])
	}
	if ms.calls != 0 || fhd.calls != 0 {
		t.Errorf("expected early return; ms=%d fhd=%d", ms.calls, fhd.calls)
	}
	if len(st.data) != 0 {
		t.Errorf("expected no cache write on error, got %d entries", len(st.data))
	}
}

func TestPrefetch_MediaSelectorError_ReturnsNilNoCacheWrite(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{err: errors.New("mediaselector down")}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if out.Heights["p1"] != nil {
		t.Errorf("expected nil result on ms error, got %v", out.Heights["p1"])
	}
	if fhd.calls != 0 {
		t.Errorf("expected no FHD call on ms error, got %d", fhd.calls)
	}
	if len(st.data) != 0 {
		t.Errorf("expected no cache write, got %d entries", len(st.data))
	}
}

func TestPrefetch_DetectsHiddenFHD(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{found: true}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	heights := out.Heights["p1"]
	if len(heights) == 0 || heights[0] != 1080 {
		t.Errorf("expected 1080 prepended to heights, got %v", heights)
	}
}

func TestPrefetch_FHDDefinitiveNo_KeepsLowerHeights(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{found: false, err: nil}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	heights := out.Heights["p1"]
	if containsInt(heights, 1080) {
		t.Errorf("expected no 1080 when FHD probe says definitive-no, got %v", heights)
	}
	if st.data["p1"] == nil {
		t.Error("expected cache write on definitive-no (cacheable)")
	}
}

// TestPrefetch_FHDProbeError_PreservesExistingHeights pins the v1.5.6
// fix: a transient FHD probe error (429, 5xx, 401/403, transport) must
// preserve the heights already resolved from mediaselector rather than
// discard the entire probe result. Pre-v1.5.6 returned nil on FHD err,
// wiping out a perfectly valid 720p/540p probe whenever the FHD HEAD
// happened to hit a throttle. We still skip the cache write so the
// next probe will retry the FHD HEAD and may discover 1080 once the
// throttle clears — matches the caching contract documented on
// fhdprobe.go::ProbeHiddenFHD.
func TestPrefetch_FHDProbeError_PreservesExistingHeights(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{err: errors.New("FHD HEAD returned 503")}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	// fakeMediaSelector default returns [720, 540]; on FHD err we keep them.
	got := out.Heights["p1"]
	if len(got) != 2 || got[0] != 720 || got[1] != 540 {
		t.Errorf("expected preserved heights [720 540] on FHD transient error, got %v", got)
	}
	if len(st.data) != 0 {
		t.Errorf("expected no cache write on transient FHD error (so next probe retries), got %d entries", len(st.data))
	}
}

func TestPrefetch_1080InManifest_VerifiesInsteadOfProbing(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: 1080, Bitrate: 3000, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 720, Bitrate: 1500, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 540, Bitrate: 700, Format: "hls", URL: "https://example.com/master.m3u8"},
		}},
	}}
	fhd := &fakeFHDProber{verifyHas1080: true, verifyMaxListed: 1080}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if fhd.calls != 0 {
		t.Errorf("expected the hidden-FHD probe to be skipped when 1080 already in manifest, got %d calls", fhd.calls)
	}
	if fhd.verifyCalls != 1 {
		t.Errorf("expected the 1080 claim to be verified once, got %d calls", fhd.verifyCalls)
	}
	heights := out.Heights["p1"]
	// Must not contain duplicate 1080.
	count1080 := 0
	for _, h := range heights {
		if h == 1080 {
			count1080++
		}
	}
	if count1080 != 1 {
		t.Errorf("expected exactly one 1080 in heights, got %d: %v", count1080, heights)
	}
}

// --- GitHub #54: mediaselector's 1080 claim is verified, not trusted ---
//
// For the iptv-all mediaset BBC returns a single video element whose
// height is the packaging profile (1920x1080 for piff_abr_full_hd), not
// an encoded rendition. Waterloo Road S06E02 reports 1080 there while
// its HLS master tops out at 720p and the FHD rewrite serves audio only.

func phantom1080MediaSelector() *fakeMediaSelector {
	return &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: 1080, Width: 1920, Bitrate: 8490, Format: "hls", URL: "https://example.com/master.m3u8"},
		}},
	}}
}

func TestPrefetch_Phantom1080_ReplacedByMasterMax(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := phantom1080MediaSelector()
	fhd := &fakeFHDProber{verifyHas1080: false, verifyMaxListed: 720}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "waterloo road"}})

	got := out.Heights["p1"]
	if len(got) != 1 || got[0] != 720 {
		t.Errorf("expected phantom 1080 replaced by the master's 720, got %v", got)
	}
	if st.data["p1"] == nil {
		t.Fatal("expected cache write on definitive verification")
	}
	if cached := st.data["p1"].Heights; len(cached) != 1 || cached[0] != 720 {
		t.Errorf("expected cached heights [720], got %v", cached)
	}
}

func TestPrefetch_Verified1080_KeepsItAndAddsMasterMax(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := phantom1080MediaSelector()
	fhd := &fakeFHDProber{verifyHas1080: true, verifyMaxListed: 720}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "waterloo road"}})

	got := out.Heights["p1"]
	if len(got) != 2 || got[0] != 1080 || got[1] != 720 {
		t.Errorf("expected [1080 720] when the hidden 1080 is real, got %v", got)
	}
}

func TestPrefetch_Verify1080TransientError_KeepsClaimSkipsCache(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := phantom1080MediaSelector()
	fhd := &fakeFHDProber{verifyErr: errors.New("FHD probe returned 503")}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "waterloo road"}})

	got := out.Heights["p1"]
	if len(got) != 1 || got[0] != 1080 {
		t.Errorf("expected the unverified claim kept for this response, got %v", got)
	}
	if len(st.data) != 0 {
		t.Errorf("expected no cache write on transient verify error, got %d entries", len(st.data))
	}
}

func TestPrefetch_Phantom1080_NoResolutionTags_KeepsClaim(t *testing.T) {
	// The master carries no RESOLUTION tags so there is no listed height
	// to fall back to; the claim is left alone rather than emptied.
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := phantom1080MediaSelector()
	fhd := &fakeFHDProber{verifyHas1080: false, verifyMaxListed: 0}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "waterloo road"}})

	got := out.Heights["p1"]
	if len(got) != 1 || got[0] != 1080 {
		t.Errorf("expected claim kept when the master lists no heights, got %v", got)
	}
}

func TestReplaceHeight(t *testing.T) {
	tests := []struct {
		name    string
		heights []int
		with    int
		want    []int
	}{
		{"single phantom", []int{1080}, 720, []int{720}},
		{"phantom above real", []int{1080, 720, 540}, 720, []int{720, 540}},
		{"replacement inserted in order", []int{1080, 540}, 720, []int{720, 540}},
		{"no replacement height", []int{1080, 720}, 0, []int{720}},
		{"nothing to remove", []int{720, 540}, 720, []int{720, 540}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceHeight(tt.heights, 1080, tt.with)
			if len(got) != len(tt.want) {
				t.Fatalf("replaceHeight(%v) = %v, want %v", tt.heights, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("replaceHeight(%v) = %v, want %v", tt.heights, got, tt.want)
				}
			}
		})
	}
}

func TestPrefetch_DASHOnlyResult_SkipsFHDProbe(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: 720, Bitrate: 1500, Format: "dash", URL: "https://example.com/manifest.mpd"},
		}},
	}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if fhd.calls != 0 {
		t.Errorf("expected FHD probe to be skipped for DASH-only, got %d calls", fhd.calls)
	}
	if len(out.Heights["p1"]) != 1 || out.Heights["p1"][0] != 720 {
		t.Errorf("expected [720], got %v", out.Heights["p1"])
	}
}

func TestPrefetch_ConcurrentDispatch_AllPIDsHandled(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 4, time.Second)

	items := make([]ProbeItem, 10)
	for i := range items {
		items[i] = ProbeItem{PID: "p" + string(rune('a'+i)), ShowName: "show"}
	}

	out := p.PrefetchPIDs(context.Background(), items)
	if len(out.Heights) != 10 {
		t.Errorf("expected 10 results, got %d", len(out.Heights))
	}
}

func TestPrefetch_ContextCancel_StopsEarly(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}, delay: 200 * time.Millisecond}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 2, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	items := []ProbeItem{{PID: "p1", ShowName: "show"}, {PID: "p2", ShowName: "show"}, {PID: "p3", ShowName: "show"}}
	_ = p.PrefetchPIDs(ctx, items)
	// No specific assertion on which PIDs returned — the test is that
	// the call returns promptly rather than hanging for the full delay.
}

func TestPrefetch_PerProbeTimeout_AbortsHangingProbe(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}, delay: 500 * time.Millisecond}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, 50*time.Millisecond)

	start := time.Now()
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})
	elapsed := time.Since(start)

	if elapsed > 300*time.Millisecond {
		t.Errorf("expected per-probe timeout (~50ms), got %v", elapsed)
	}
	if out.Heights["p1"] != nil {
		t.Errorf("expected nil result on timeout, got %v", out.Heights["p1"])
	}
}

func TestPrefetch_DeduplicatesAndSortsHeights(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: 540, Bitrate: 500, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 720, Bitrate: 1000, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 540, Bitrate: 550, Format: "hls", URL: "https://example.com/master.m3u8"},  // duplicate
			{Height: 720, Bitrate: 1100, Format: "hls", URL: "https://example.com/master.m3u8"}, // duplicate
		}},
	}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})
	heights := out.Heights["p1"]

	// Should be [720, 540] — deduped and descending.
	if len(heights) != 2 {
		t.Errorf("expected 2 heights after dedupe, got %d: %v", len(heights), heights)
	}
	if !sort.SliceIsSorted(heights, func(i, j int) bool { return heights[i] > heights[j] }) {
		t.Errorf("expected descending sort, got %v", heights)
	}
}

// --- show-group dedup tests ---

func TestPrefetch_ShowGroupDedup_ProbesOncePerShow(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 8, time.Second)

	// 3 shows, 5 PIDs each = 15 items total.
	var items []ProbeItem
	for _, show := range []string{"eastenders", "holby city", "casualty"} {
		for i := 0; i < 5; i++ {
			items = append(items, ProbeItem{
				PID:      show[:2] + "_p" + string(rune('0'+i)),
				ShowName: show,
			})
		}
	}

	out := p.PrefetchPIDs(context.Background(), items)

	if len(out.Heights) != 15 {
		t.Fatalf("expected 15 results, got %d", len(out.Heights))
	}
	for _, item := range items {
		if out.Heights[item.PID] == nil {
			t.Errorf("missing result for PID %s", item.PID)
		}
	}

	// Only 3 playlist calls (one per show), not 15.
	if pl.calls != 3 {
		t.Errorf("expected 3 playlist calls (one per show), got %d", pl.calls)
	}
	if ms.calls != 3 {
		t.Errorf("expected 3 mediaselector calls (one per show), got %d", ms.calls)
	}

	// All 15 PIDs should be in the cache.
	if len(st.data) != 15 {
		t.Errorf("expected 15 cache entries, got %d", len(st.data))
	}
}

func TestPrefetch_ShowGroupDedup_FirstFails_FallsBackToIndividual(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	// p0's VPID resolves to empty streams, so probeOne returns nil for it.
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p0": {Video: []VideoStream{}},
	}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 8, time.Second)

	items := []ProbeItem{
		{PID: "p0", ShowName: "show"},
		{PID: "p1", ShowName: "show"},
		{PID: "p2", ShowName: "show"},
	}

	out := p.PrefetchPIDs(context.Background(), items)

	// Leader p0 failed, but fallback probed p1/p2 individually.
	// The group gets the first successful sibling's heights (all
	// episodes of a show share the same qualities).
	if out.Heights["p1"] == nil {
		t.Fatal("expected p1 to have heights after fallback probe")
	}
	if out.Heights["p2"] == nil {
		t.Fatal("expected p2 to have heights after fallback probe")
	}
	// p0 gets the same group result -- the show's qualities are valid
	// even though p0's individual probe failed.
	if out.Heights["p0"] == nil {
		t.Error("expected p0 to inherit sibling heights")
	}
}

func TestPrefetch_ShowGroupDedup_AllFail_ReturnsNil(t *testing.T) {
	// When every PID in a show group fails, the group result is nil.
	pl := &fakePlaylistResolver{err: errors.New("playlist down")}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 8, time.Second)

	items := []ProbeItem{
		{PID: "p0", ShowName: "show"},
		{PID: "p1", ShowName: "show"},
	}

	out := p.PrefetchPIDs(context.Background(), items)

	for _, pid := range []string{"p0", "p1"} {
		if out.Heights[pid] != nil {
			t.Errorf("expected nil for %s when all probes fail, got %v", pid, out.Heights[pid])
		}
	}
}

func TestPrefetch_ShowGroupDedup_CacheHitCoversGroup(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	// Pre-populate cache for p0.
	st.data["p0"] = &store.QualityCache{
		PID:      "p0",
		ShowName: "show",
		Heights:  []int{720, 540},
		ProbedAt: time.Now(),
	}

	p := NewQualityProber(pl, ms, fhd, st, 8, time.Second)

	items := []ProbeItem{
		{PID: "p0", ShowName: "show"},
		{PID: "p1", ShowName: "show"},
		{PID: "p2", ShowName: "show"},
	}

	out := p.PrefetchPIDs(context.Background(), items)

	// Zero HTTP calls -- cache hit for p0 covers the whole group.
	if pl.calls != 0 {
		t.Errorf("expected 0 playlist calls (cache hit covers group), got %d", pl.calls)
	}
	if ms.calls != 0 {
		t.Errorf("expected 0 mediaselector calls, got %d", ms.calls)
	}

	// All 3 should have [720, 540].
	for _, pid := range []string{"p0", "p1", "p2"} {
		h := out.Heights[pid]
		if len(h) != 2 || h[0] != 720 || h[1] != 540 {
			t.Errorf("PID %s: expected [720, 540], got %v", pid, h)
		}
	}

	// All 3 should be in the cache.
	if len(st.data) != 3 {
		t.Errorf("expected 3 cache entries, got %d", len(st.data))
	}
}

// --- not-yet-available (Issue #44) ---

// TestPrefetch_NotYetAvailable_MarksAndSkipsCache verifies that a playlist
// resolve returning ErrNotYetAvailable marks the PID NotYetAvailable, leaves
// its Heights nil, and writes nothing to the cache (so the next probe
// re-checks once BBC publishes the stream).
func TestPrefetch_NotYetAvailable_MarksAndSkipsCache(t *testing.T) {
	pl := &fakePlaylistResolver{err: ErrNotYetAvailable}
	ms := &fakeMediaSelector{}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if !out.NotYetAvailable["p1"] {
		t.Error("expected p1 marked NotYetAvailable")
	}
	if out.Heights["p1"] != nil {
		t.Errorf("expected nil heights for not-yet-available PID, got %v", out.Heights["p1"])
	}
	if st.writes != 0 {
		t.Errorf("expected no cache write for not-yet-available PID, got %d writes", st.writes)
	}
	if ms.calls != 0 || fhd.calls != 0 {
		t.Errorf("expected early return; ms=%d fhd=%d", ms.calls, fhd.calls)
	}
}

// TestPrefetch_GenericError_NotMarkedNotYetAvailable verifies that a
// generic (non-sentinel) playlist error leaves NotYetAvailable false and
// Heights nil, preserving the transient/fallback path the indexer relies on.
func TestPrefetch_GenericError_NotMarkedNotYetAvailable(t *testing.T) {
	pl := &fakePlaylistResolver{err: errors.New("playlist down")}
	ms := &fakeMediaSelector{}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if out.NotYetAvailable["p1"] {
		t.Error("generic error must not mark NotYetAvailable")
	}
	if out.Heights["p1"] != nil {
		t.Errorf("expected nil heights on generic error, got %v", out.Heights["p1"])
	}
}

// GitHub #57: a PID that only has an audio-described or signed version
// probes that version alone (audio-described is 396p only). Its heights
// must not go into the no-TTL cache, for the PID or its show siblings.
func TestPrefetch_AccessibilityOnlyIsNotCached(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{
		"ad1": {VPID: "vpid-ad1", AccessibilityOnly: true},
	}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-ad1": {Video: []VideoStream{{Height: 396, Bitrate: 500, Format: "hls", URL: "https://example.com/ad.m3u8", Supplier: "mf_akamai"}}},
	}}
	st := newFakeCacheStore()
	p := NewQualityProber(pl, ms, &fakeFHDProber{}, st, 1, time.Second)

	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "ad1", ShowName: "signed show"}, {PID: "ad2", ShowName: "signed show"}})

	if got := out.Heights["ad1"]; len(got) != 1 || got[0] != 396 {
		t.Errorf("heights = %v, want [396] for this response", got)
	}
	if st.writes != 0 {
		t.Errorf("cache writes = %d, want 0", st.writes)
	}
}

// Just after a broadcast, mediaselector can serve the new version only
// from live-channel suppliers at 720p, before the 1080p on-demand files
// exist. Caching that would hide the 1080p for good.
func TestPrefetch_LiveSuppliersAreNotCached(t *testing.T) {
	cases := []struct {
		name      string
		suppliers []string
		wantCache bool
	}{
		{"all live", []string{"akamai_live_uk", "bbc_bidi_live_uk"}, false},
		{"mixed", []string{"mf_akamai", "akamai_live_uk"}, true},
		{"mixed, live first", []string{"akamai_live_uk", "mf_akamai"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var video []VideoStream
			for _, s := range tc.suppliers {
				video = append(video, VideoStream{Height: 720, Bitrate: 5000, Format: "hls", URL: "https://example.com/" + s + ".m3u8", Supplier: s})
			}
			pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
			ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{"vpid-pv1": {Video: video}}}
			st := newFakeCacheStore()
			p := NewQualityProber(pl, ms, &fakeFHDProber{}, st, 1, time.Second)

			out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "pv1", ShowName: "show"}})

			if got := out.Heights["pv1"]; len(got) == 0 || got[0] != 720 {
				t.Errorf("heights = %v, want 720 first", got)
			}
			if cached := st.data["pv1"] != nil; cached != tc.wantCache {
				t.Errorf("cached = %v, want %v", cached, tc.wantCache)
			}
		})
	}
}

// The real resolver against iBL captures: a live programme reads as not
// yet available and the BBC error page as a transient failure; neither
// is cached. GitHub #57.
func TestPrefetch_RealResolverOutcomesAreNotCached(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		srv := newIBLEpisodeServer(t, map[string]string{"m00334lv": "episode_m00334lv.json"})
		st := newFakeCacheStore()
		p := NewQualityProber(newFixtureResolver(srv.URL, fixtureNow), &fakeMediaSelector{}, &fakeFHDProber{}, st, 1, time.Second)

		out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "m00334lv", ShowName: "Politics Live"}})
		if !out.NotYetAvailable["m00334lv"] {
			t.Error("a live programme must be reported not yet available")
		}
		if st.writes != 0 {
			t.Errorf("cache writes = %d, want 0", st.writes)
		}
	})
	t.Run("error page", func(t *testing.T) {
		srv, _ := newErrorPageServer(t)
		st := newFakeCacheStore()
		p := NewQualityProber(newFixtureResolver(srv.URL, fixtureNow), &fakeMediaSelector{}, &fakeFHDProber{}, st, 1, time.Second)

		out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "m0033jgh", ShowName: "UEFA Champions League"}})
		if out.Heights["m0033jgh"] != nil || out.NotYetAvailable["m0033jgh"] {
			t.Errorf("heights = %v, nya = %v; want a transient failure", out.Heights["m0033jgh"], out.NotYetAvailable["m0033jgh"])
		}
		if st.writes != 0 {
			t.Errorf("cache writes = %d, want 0", st.writes)
		}
	})
}

// The live-supplier guard against real mediaselector replies: Politics
// Live (m00334lt) minutes after broadcast, served only by *_live_uk
// suppliers, and the #57 clip (m0033jgj) served by on-demand suppliers.
func TestLiveSuppliersOnly_RealMediaselectorReplies(t *testing.T) {
	ms := NewMediaSelector(NewClient())
	cases := map[string]bool{
		"mediaselector_m00334lt_after_broadcast.xml": true,
		"mediaselector_m0033jgj.xml":                 false,
	}
	for file, want := range cases {
		body, err := os.ReadFile("testdata/ibl/" + file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		set, err := ms.parseResponse(body)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		if len(set.Video) == 0 {
			t.Fatalf("%s: no video streams", file)
		}
		if got := liveSuppliersOnly(set.Video); got != want {
			t.Errorf("%s: liveSuppliersOnly = %v, want %v", file, got, want)
		}
	}
}

// --- Gitea #91: FHD check fails over between HLS suppliers ---

const (
	fhdSupplierA = "https://cdn-a.example/master.m3u8"
	fhdSupplierB = "https://cdn-b.example/master.m3u8"
)

// perURLFHDProber answers per master URL, so a test can make one supplier
// fail while the next one answers. It records the URLs asked, in order.
type perURLFHDProber struct {
	mu         sync.Mutex
	errByURL   map[string]error
	has1080    bool
	maxListed  int
	found      bool
	verifyURLs []string
	probeURLs  []string
}

func (f *perURLFHDProber) VerifyFHD(ctx context.Context, hlsMasterURL string) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyURLs = append(f.verifyURLs, hlsMasterURL)
	if err := f.errByURL[hlsMasterURL]; err != nil {
		return false, 0, err
	}
	return f.has1080, f.maxListed, nil
}

func (f *perURLFHDProber) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeURLs = append(f.probeURLs, hlsMasterURL)
	if err := f.errByURL[hlsMasterURL]; err != nil {
		return "", false, err
	}
	return "https://cdn.example/stream-video=12000000.m3u8", f.found, nil
}

// twoSupplierStreams is one rendition offered by two suppliers at the same
// bitrate, in mediaselector order, the layout of
// testdata/ibl/mediaselector_m0033jgj.xml (mf_akamai, then mf_cloudfront).
func twoSupplierStreams(height int) *fakeMediaSelector {
	return &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: height, Width: height * 16 / 9, Bitrate: 8490, Format: "hls", Supplier: "mf_akamai", URL: fhdSupplierA},
			{Height: height, Width: height * 16 / 9, Bitrate: 8490, Format: "hls", Supplier: "mf_cloudfront", URL: fhdSupplierB},
		}},
	}}
}

// The first supplier refuses the 1080 verification; the second answers
// that the master tops out at 720p. The phantom 1080 must be replaced and
// the answer cached, not kept unverified.
func TestPrefetch_FHDVerifyFailsOverToNextSupplier(t *testing.T) {
	fhd := &perURLFHDProber{
		errByURL:  map[string]error{fhdSupplierA: errors.New("FHD probe returned 403")},
		maxListed: 720,
	}
	st := newFakeCacheStore()
	p := NewQualityProber(&fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}, twoSupplierStreams(1080), fhd, st, 1, time.Second)

	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if got := out.Heights["p1"]; len(got) != 1 || got[0] != 720 {
		t.Errorf("heights = %v, want [720] from the second supplier's answer", got)
	}
	if st.data["p1"] == nil {
		t.Error("a definitive answer from the second supplier must be cached")
	}
	if want := []string{fhdSupplierA, fhdSupplierB}; !equalStrings(fhd.verifyURLs, want) {
		t.Errorf("VerifyFHD asked %v, want %v", fhd.verifyURLs, want)
	}
}

// Same for the hidden-1080p probe on a 720p listing.
func TestPrefetch_HiddenFHDProbeFailsOverToNextSupplier(t *testing.T) {
	fhd := &perURLFHDProber{
		errByURL: map[string]error{fhdSupplierA: errors.New("master playlist fetch: HTTP 503")},
		found:    true,
	}
	st := newFakeCacheStore()
	p := NewQualityProber(&fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}, twoSupplierStreams(720), fhd, st, 1, time.Second)

	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if got := out.Heights["p1"]; len(got) != 2 || got[0] != 1080 || got[1] != 720 {
		t.Errorf("heights = %v, want [1080 720]", got)
	}
	if st.data["p1"] == nil {
		t.Error("a definitive answer from the second supplier must be cached")
	}
	if want := []string{fhdSupplierA, fhdSupplierB}; !equalStrings(fhd.probeURLs, want) {
		t.Errorf("ProbeHiddenFHD asked %v, want %v", fhd.probeURLs, want)
	}
}

// Review focus: when every supplier errors, every one is tried, the
// claim is kept for this response, and nothing is cached (the existing
// transient contract).
func TestPrefetch_FHDVerifyAllSuppliersFail_KeepsClaimSkipsCache(t *testing.T) {
	fhd := &perURLFHDProber{errByURL: map[string]error{
		fhdSupplierA: errors.New("FHD probe returned 403"),
		fhdSupplierB: errors.New("FHD probe returned 503"),
	}}
	st := newFakeCacheStore()
	p := NewQualityProber(&fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}, twoSupplierStreams(1080), fhd, st, 1, time.Second)

	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if got := out.Heights["p1"]; len(got) != 1 || got[0] != 1080 {
		t.Errorf("heights = %v, want the unverified [1080] kept for this response", got)
	}
	if st.writes != 0 {
		t.Errorf("cache writes = %d, want 0 when every supplier errored", st.writes)
	}
	if want := []string{fhdSupplierA, fhdSupplierB}; !equalStrings(fhd.verifyURLs, want) {
		t.Errorf("VerifyFHD asked %v, want %v", fhd.verifyURLs, want)
	}
}

// A healthy first supplier is the only one asked: failover costs nothing
// when nothing fails.
func TestPrefetch_FHDFirstSupplierAnswers_NoFailover(t *testing.T) {
	fhd := &perURLFHDProber{maxListed: 720}
	p := NewQualityProber(&fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}, twoSupplierStreams(1080), fhd, newFakeCacheStore(), 1, time.Second)

	_ = p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if want := []string{fhdSupplierA}; !equalStrings(fhd.verifyURLs, want) {
		t.Errorf("VerifyFHD asked %v, want %v", fhd.verifyURLs, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
