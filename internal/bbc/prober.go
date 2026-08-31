package bbc

import (
	"context"
	"errors"
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

// PrefetchResult carries per-PID probe outcomes. Heights holds usable quality
// heights for PIDs whose probe succeeded. NotYetAvailable lists PIDs whose
// playlist was empty (ErrNotYetAvailable), the caller must skip advertising
// them rather than fall back to a default quality, and they are deliberately
// NOT cached so the next probe re-checks once BBC publishes. A PID absent from
// both (nil heights, not in NotYetAvailable) is a transient failure: the caller
// keeps its existing fallback behaviour. Issue #44.
type PrefetchResult struct {
	Heights         map[string][]int
	NotYetAvailable map[string]bool
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
// QualityProber.concurrency), returns a PrefetchResult. Cache hits skip the
// HTTP work entirely. Probe failures map to a nil Heights entry (not a missing
// key) so the caller can distinguish "probed and failed" from "not yet
// probed"; a not-yet-available playlist is flagged in NotYetAvailable and
// never cached. Honours ctx.
func (p *QualityProber) PrefetchPIDs(ctx context.Context, items []ProbeItem) PrefetchResult {
	result := PrefetchResult{
		Heights:         make(map[string][]int, len(items)),
		NotYetAvailable: make(map[string]bool),
	}
	var mu sync.Mutex

	// Group items by ShowName so we can probe one PID per show and
	// reuse the result for all siblings. Within a BBC show, all
	// episodes share the same available qualities.
	groups := make(map[string][]ProbeItem, len(items))
	var order []string
	for _, item := range items {
		if _, exists := groups[item.ShowName]; !exists {
			order = append(order, item.ShowName)
		}
		groups[item.ShowName] = append(groups[item.ShowName], item)
	}

	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, showName := range order {
		group := groups[showName]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			heights, nya := p.probeShowGroup(ctx, group)
			mu.Lock()
			for _, item := range group {
				result.Heights[item.PID] = heights
				if nya {
					result.NotYetAvailable[item.PID] = true
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

// PrefetchPIDsIndividually probes every item on its own instead of
// electing one leader per ShowName group, and returns the same
// PrefetchResult shape. Duplicate PIDs are probed once.
//
// PrefetchPIDs is the right call for the newznab feed: a whole BBC brand
// shares one quality set, so one probe answers for the lot. It cannot
// answer "is THIS episode published yet", because probeShowGroup returns
// the leader's verdict (or any sibling's cache hit) for the entire
// group. For a show with several published episodes plus a newest one
// BBC has not published, that reports the unpublished episode as
// available. Callers that need a per-PID availability answer rather than
// a per-show quality set use this instead. Issue #52.
//
// The per-PID cache short-circuit in probeOne still applies, so a PID
// whose cache row was written by a previous grouped probe reads as
// available without a fresh playlist fetch.
//
// Honours ctx and the same concurrency bound as PrefetchPIDs.
func (p *QualityProber) PrefetchPIDsIndividually(ctx context.Context, items []ProbeItem) PrefetchResult {
	result := PrefetchResult{
		Heights:         make(map[string][]int, len(items)),
		NotYetAvailable: make(map[string]bool),
	}
	var mu sync.Mutex

	seen := make(map[string]struct{}, len(items))
	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		if _, dup := seen[item.PID]; dup {
			continue
		}
		seen[item.PID] = struct{}{}

		wg.Add(1)
		sem <- struct{}{}
		go func(item ProbeItem) {
			defer wg.Done()
			defer func() { <-sem }()

			heights, nya := p.probeOne(ctx, item)
			mu.Lock()
			result.Heights[item.PID] = heights
			if nya {
				result.NotYetAvailable[item.PID] = true
			}
			mu.Unlock()
		}(item)
	}
	wg.Wait()
	return result
}

// probeShowGroup probes a group of PIDs that share a ShowName. It
// checks for a cache hit first, then probes the first PID via
// probeOne and reuses its result for the rest. If the first PID
// fails, falls back to probing remaining items individually.
func (p *QualityProber) probeShowGroup(ctx context.Context, group []ProbeItem) (heights []int, notYetAvailable bool) {
	// 1. Check if any PID in the group is already cached.
	for _, item := range group {
		if cached, err := p.store.GetQualityCache(item.PID); err == nil && cached != nil {
			for _, sibling := range group {
				if sibling.PID == item.PID {
					continue
				}
				_ = p.store.PutQualityCache(&store.QualityCache{
					PID:      sibling.PID,
					ShowName: sibling.ShowName,
					Heights:  cached.Heights,
					ProbedAt: cached.ProbedAt,
				})
			}
			log.Printf("show-group cache hit: show=%q leader=%s heights=%v siblings=%d",
				item.ShowName, item.PID, cached.Heights, len(group)-1)
			return cached.Heights, false
		}
	}

	// 2. Probe the first PID as the representative for the group. Capture
	// the leader's not-yet-available flag so a single-PID group (the common
	// daily-show case) still surfaces it after the fallback loop.
	leaderHeights, leaderNYA := p.probeOne(ctx, group[0])

	if leaderHeights != nil {
		now := time.Now()
		for _, sibling := range group[1:] {
			_ = p.store.PutQualityCache(&store.QualityCache{
				PID:      sibling.PID,
				ShowName: sibling.ShowName,
				Heights:  leaderHeights,
				ProbedAt: now,
			})
		}
		log.Printf("show-group probed: show=%q leader=%s heights=%v siblings=%d",
			group[0].ShowName, group[0].PID, leaderHeights, len(group)-1)
		return leaderHeights, false
	}

	// 3. First PID failed -- fall back to individual probing. A not-yet-
	// available OR transient leader must not suppress siblings that ARE
	// available, so probe them individually and keep the first success.
	log.Printf("show-group leader failed: show=%q pid=%s, probing %d siblings individually",
		group[0].ShowName, group[0].PID, len(group)-1)
	var fallbackHeights []int
	anyNotYetAvailable := leaderNYA
	for _, sibling := range group[1:] {
		h, nya := p.probeOne(ctx, sibling)
		if h != nil && fallbackHeights == nil {
			fallbackHeights = h
		}
		if nya {
			anyNotYetAvailable = true
		}
	}
	if fallbackHeights != nil {
		return fallbackHeights, false
	}
	return nil, anyNotYetAvailable
}

// probeOne runs the full probe for a single item. Returns the heights
// slice on success (possibly empty if BBC has no streams), or nil on
// any error (cached entries are never nil, but the result-map entry
// is nil to signal "probe attempted, no usable answer"). The second
// return value is true only when the playlist was empty
// (ErrNotYetAvailable); that branch is never cached so the next probe
// re-checks once BBC publishes the stream. Issue #44.
func (p *QualityProber) probeOne(parentCtx context.Context, item ProbeItem) (heights []int, notYetAvailable bool) {
	// 1. Cache hit short-circuit.
	if cached, err := p.store.GetQualityCache(item.PID); err == nil && cached != nil {
		return cached.Heights, false
	}

	// 2. Per-probe deadline bounded by the parent context.
	probeCtx, cancel := context.WithTimeout(parentCtx, p.timeout)
	defer cancel()

	// 3. playlist PID -> VPID
	plInfo, err := p.playlist.ResolveCtx(probeCtx, item.PID)
	if err != nil {
		if errors.Is(err, ErrNotYetAvailable) {
			log.Printf("quality probe: not yet available pid=%s (skipping advertise, no cache)", item.PID)
			return nil, true
		}
		log.Printf("quality probe failed pid=%s err=%v (playlist)", item.PID, err)
		return nil, false
	}
	if plInfo.VPID == "" {
		log.Printf("quality probe failed pid=%s err=no-vpid", item.PID)
		return nil, false
	}

	// 4. mediaselector VPID -> streams; walk heights, dedupe, sort descending.
	streams, err := p.ms.ResolveCtx(probeCtx, plInfo.VPID)
	if err != nil {
		log.Printf("quality probe failed pid=%s err=%v (mediaselector)", item.PID, err)
		return nil, false
	}
	heights = dedupedSortedHeights(streams.Video)

	// 5. FHD probe (skipped if 1080 already present, or if the best
	// available resolution is below 720p -- SD-only content never has
	// hidden 1080p, and skipping saves a master playlist HTTP fetch).
	//
	// FHD probe errors are TRANSIENT (429, 5xx, 401/403, transport
	// failures — see internal/bbc/fhdprobe.go ProbeHiddenFHD doc) and
	// must NOT discard the heights we already resolved from
	// mediaselector. Pre-v1.5.6 returned nil on FHD err, wiping out a
	// perfectly valid 720p/540p quality probe whenever the FHD HEAD
	// happened to hit a throttle. We keep the existing heights for
	// this response BUT skip the cache write so the next probe will
	// retry the FHD HEAD and may discover 1080p once the throttle
	// clears. Matches both the downloader pattern at
	// internal/download/ffmpeg.go ~181 (log + fall through to bestURL)
	// and the caching contract in ProbeHiddenFHD's doc string
	// ("Callers that cache must NOT cache this branch").
	fhdProbeTransientErr := false
	if !containsInt(heights, 1080) && len(heights) > 0 && heights[0] >= 720 {
		if bestHLS := pickBestHLSURL(streams.Video); bestHLS != "" {
			_, found, err := p.fhdProber.ProbeHiddenFHD(probeCtx, bestHLS)
			switch {
			case err != nil:
				log.Printf("quality probe: fhd transient err pid=%s err=%v (keeping %v, skipping cache)", item.PID, err, heights)
				fhdProbeTransientErr = true
			case found:
				heights = append([]int{1080}, heights...)
			}
		}
	}

	// 6. Persist — but skip the cache write when the FHD probe hit a
	// transient error so the next call retries the FHD HEAD instead
	// of locking in "no 1080" until the cache TTL expires. The
	// already-resolved heights are still returned for this response.
	if !fhdProbeTransientErr {
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
	}

	// 7. Log success at INFO level via the ring buffer (any existing logger).
	log.Printf("quality probe pid=%s heights=%v", item.PID, heights)
	return heights, false
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
