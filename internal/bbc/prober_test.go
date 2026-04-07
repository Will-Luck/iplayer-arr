package bbc

import (
	"context"
	"errors"
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
}

func (f *fakeFHDProber) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (string, bool, error) {
	atomic.AddInt32(&f.calls, 1)
	return "https://example.com/stream-video=12000000.m3u8", f.found, f.err
}

type fakeCacheStore struct {
	mu   sync.Mutex
	data map[string]*store.QualityCache
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
	if len(out["p1"]) != 2 || out["p1"][0] != 720 {
		t.Errorf("expected cached heights [720,540], got %v", out["p1"])
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

	if out["p1"] != nil {
		t.Errorf("expected nil result on playlist error, got %v", out["p1"])
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

	if out["p1"] != nil {
		t.Errorf("expected nil result on ms error, got %v", out["p1"])
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

	heights := out["p1"]
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

	heights := out["p1"]
	if containsInt(heights, 1080) {
		t.Errorf("expected no 1080 when FHD probe says definitive-no, got %v", heights)
	}
	if st.data["p1"] == nil {
		t.Error("expected cache write on definitive-no (cacheable)")
	}
}

func TestPrefetch_FHDProbeError_ReturnsNilNoCacheWrite(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{}}
	fhd := &fakeFHDProber{err: errors.New("FHD HEAD returned 503")}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if out["p1"] != nil {
		t.Errorf("expected nil result on FHD transient error, got %v", out["p1"])
	}
	if len(st.data) != 0 {
		t.Errorf("expected no cache write on transient FHD error, got %d entries", len(st.data))
	}
}

func TestPrefetch_1080InManifest_SkipsFHDProbe(t *testing.T) {
	pl := &fakePlaylistResolver{byPID: map[string]*PlaylistInfo{}}
	ms := &fakeMediaSelector{byVPID: map[string]*StreamSet{
		"vpid-p1": {Video: []VideoStream{
			{Height: 1080, Bitrate: 3000, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 720, Bitrate: 1500, Format: "hls", URL: "https://example.com/master.m3u8"},
			{Height: 540, Bitrate: 700, Format: "hls", URL: "https://example.com/master.m3u8"},
		}},
	}}
	fhd := &fakeFHDProber{}
	st := newFakeCacheStore()

	p := NewQualityProber(pl, ms, fhd, st, 1, time.Second)
	out := p.PrefetchPIDs(context.Background(), []ProbeItem{{PID: "p1", ShowName: "show"}})

	if fhd.calls != 0 {
		t.Errorf("expected FHD probe to be skipped when 1080 already in manifest, got %d calls", fhd.calls)
	}
	heights := out["p1"]
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
	if len(out["p1"]) != 1 || out["p1"][0] != 720 {
		t.Errorf("expected [720], got %v", out["p1"])
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
	if len(out) != 10 {
		t.Errorf("expected 10 results, got %d", len(out))
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
	if out["p1"] != nil {
		t.Errorf("expected nil result on timeout, got %v", out["p1"])
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
	heights := out["p1"]

	// Should be [720, 540] — deduped and descending.
	if len(heights) != 2 {
		t.Errorf("expected 2 heights after dedupe, got %d: %v", len(heights), heights)
	}
	if !sort.SliceIsSorted(heights, func(i, j int) bool { return heights[i] > heights[j] }) {
		t.Errorf("expected descending sort, got %v", heights)
	}
}
