package bbc

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// QualityProber probes BBC playlist + mediaselector + hidden FHD for a
// list of PIDs, caches the resulting heights in BoltDB, and returns
// a map of PID -> heights. Designed to be called from the search
// handler's single-pass walk before the emit loop runs.
type QualityProber struct {
	playlist    pidToVPIDResolver
	ms          vpidToStreamsResolver
	fhdProber   fhdProber
	store       qualityCacheStore
	concurrency int
	timeout     time.Duration
}

// Narrow local interfaces so prober_test.go can inject fakes without
// depending on concrete bbc.Client, bbc.PlaylistResolver, bbc.MediaSelector,
// or *store.Store. Concrete types satisfy these automatically via Go's
// structural typing.
type pidToVPIDResolver interface {
	ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error)
}

type vpidToStreamsResolver interface {
	ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error)
}

type fhdProber interface {
	ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error)
}

type qualityCacheStore interface {
	GetQualityCache(pid string) (*store.QualityCache, error)
	PutQualityCache(qc *store.QualityCache) error
}

// ProbeItem is one input to PrefetchPIDs. The ShowName is used for
// cache persistence (so a future DeleteQualityCacheByShow can find
// related entries); the prober itself does not filter by ShowName.
type ProbeItem struct {
	PID      string
	ShowName string
}

// NewQualityProber constructs a prober with the given dependencies.
// concurrency defaults to 8 if <= 0; timeout defaults to 20s if <= 0.
func NewQualityProber(
	playlist pidToVPIDResolver,
	ms vpidToStreamsResolver,
	fhd fhdProber,
	st qualityCacheStore,
	concurrency int,
	timeout time.Duration,
) *QualityProber {
	if concurrency <= 0 {
		concurrency = 8
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &QualityProber{
		playlist:    playlist,
		ms:          ms,
		fhdProber:   fhd,
		store:       st,
		concurrency: concurrency,
		timeout:     timeout,
	}
}

// PrefetchPIDs probes the given items in parallel (bounded by
// QualityProber.concurrency), returns a map of PID -> heights. Cache
// hits skip the HTTP work entirely. Probe failures map to a nil
// result map entry (not a missing key) so the caller can distinguish
// "probed and failed" from "not yet probed". Honours ctx.
func (p *QualityProber) PrefetchPIDs(ctx context.Context, items []ProbeItem) map[string][]int {
	result := make(map[string][]int, len(items))
	var mu sync.Mutex

	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			heights := p.probeOne(ctx, item)
			mu.Lock()
			result[item.PID] = heights
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

// probeOne runs the full probe for a single item. Returns the heights
// slice on success (possibly empty if BBC has no streams), or nil on
// any error (cached entries are never nil, but the result-map entry
// is nil to signal "probe attempted, no usable answer").
func (p *QualityProber) probeOne(parentCtx context.Context, item ProbeItem) []int {
	// 1. Cache hit short-circuit.
	if cached, err := p.store.GetQualityCache(item.PID); err == nil && cached != nil {
		return cached.Heights
	}

	// 2. Per-probe deadline bounded by the parent context.
	probeCtx, cancel := context.WithTimeout(parentCtx, p.timeout)
	defer cancel()

	// 3. playlist PID -> VPID
	plInfo, err := p.playlist.ResolveCtx(probeCtx, item.PID)
	if err != nil {
		log.Printf("quality probe failed pid=%s err=%v (playlist)", item.PID, err)
		return nil
	}
	if plInfo.VPID == "" {
		log.Printf("quality probe failed pid=%s err=no-vpid", item.PID)
		return nil
	}

	// 4. mediaselector VPID -> streams; walk heights, dedupe, sort descending.
	streams, err := p.ms.ResolveCtx(probeCtx, plInfo.VPID)
	if err != nil {
		log.Printf("quality probe failed pid=%s err=%v (mediaselector)", item.PID, err)
		return nil
	}
	heights := dedupedSortedHeights(streams.Video)

	// 5. FHD probe (skipped if 1080 already present in manifest).
	if !containsInt(heights, 1080) {
		if bestHLS := pickBestHLSURL(streams.Video); bestHLS != "" {
			_, found, err := p.fhdProber.ProbeHiddenFHD(probeCtx, bestHLS)
			if err != nil {
				log.Printf("quality probe failed pid=%s err=%v (fhd)", item.PID, err)
				return nil
			}
			if found {
				heights = append([]int{1080}, heights...)
			}
		}
	}

	// 6. Persist. PutQualityCache normalises ShowName internally.
	if err := p.store.PutQualityCache(&store.QualityCache{
		PID:      item.PID,
		ShowName: item.ShowName,
		Heights:  heights,
		ProbedAt: time.Now(),
	}); err != nil {
		log.Printf("quality probe cache write failed pid=%s err=%v", item.PID, err)
		// Fall through — the result is still usable for this response
		// even if the cache write failed.
	}

	// 7. Log success at INFO level via the ring buffer (any existing logger).
	log.Printf("quality probe pid=%s heights=%v", item.PID, heights)
	return heights
}

// dedupedSortedHeights extracts unique Height values from the VideoStream
// slice, sorts descending, and returns. Zero heights are dropped.
func dedupedSortedHeights(streams []VideoStream) []int {
	seen := make(map[int]struct{}, len(streams))
	var out []int
	for _, s := range streams {
		if s.Height <= 0 {
			continue
		}
		if _, dup := seen[s.Height]; dup {
			continue
		}
		seen[s.Height] = struct{}{}
		out = append(out, s.Height)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// pickBestHLSURL returns the URL of the highest-bitrate HLS stream in
// the slice, or "" if none is present.
func pickBestHLSURL(streams []VideoStream) string {
	bestBitrate := 0
	bestURL := ""
	for _, s := range streams {
		if s.Format != "hls" {
			continue
		}
		if s.Bitrate > bestBitrate {
			bestBitrate = s.Bitrate
			bestURL = s.URL
		}
	}
	return bestURL
}

func containsInt(haystack []int, needle int) bool {
	for _, n := range haystack {
		if n == needle {
			return true
		}
	}
	return false
}
