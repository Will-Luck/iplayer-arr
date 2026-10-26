package bbc

import (
	"context"
	"errors"
	"log"
	"slices"
	"sort"
	"strings"
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
	VerifyFHD(ctx context.Context, hlsMasterURL string) (has1080 bool, maxListed int, err error)
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
// The per-PID cache short-circuit in probeOne still applies to rows a
// probe of that PID wrote. A row probeShowGroup propagated to a sibling
// it never probed is a miss there, so it cannot report an unpublished
// episode as available. Gitea #75.
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

			heights, nya, _ := p.probeOne(ctx, item)
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
	// 1. Check if any PID in the group is already cached. A propagated
	// row counts: the feed only needs the show's heights. Gitea #75.
	for _, item := range group {
		if cached, err := p.store.GetQualityCache(item.PID); err == nil && cached != nil {
			p.propagateToSiblings(group, item.PID, cached.Heights, cached.ProbedAt)
			log.Printf("show-group cache hit: show=%q leader=%s heights=%v siblings=%d",
				item.ShowName, item.PID, cached.Heights, len(group)-1)
			return cached.Heights, false
		}
	}

	// 2. Probe the first PID as the representative for the group. Capture
	// the leader's not-yet-available flag so a single-PID group (the common
	// daily-show case) still surfaces it after the fallback loop.
	leaderHeights, leaderNYA, leaderCached := p.probeOne(ctx, group[0])

	if leaderHeights != nil {
		// Siblings inherit the leader's cache row only when the leader's
		// own answer was cacheable; heights probeOne kept out of the cache
		// (a transient FHD error, an accessibility-only version, live-only
		// suppliers) must not reach the cache through the group either.
		if leaderCached {
			p.propagateToSiblings(group, group[0].PID, leaderHeights, time.Now())
		}
		log.Printf("show-group probed: show=%q leader=%s heights=%v siblings=%d cached=%v",
			group[0].ShowName, group[0].PID, leaderHeights, len(group)-1, leaderCached)
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
		h, nya, _ := p.probeOne(ctx, sibling)
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

// propagateToSiblings writes heights as a propagated cache row for every
// group member except fromPID, so the newznab feed can reuse the show's
// heights without probing each episode. A sibling that already has a row
// from its own probe keeps it. probeOne treats a propagated row as a
// miss, so these rows never answer per-PID availability. Gitea #75.
func (p *QualityProber) propagateToSiblings(group []ProbeItem, fromPID string, heights []int, probedAt time.Time) {
	for _, sibling := range group {
		if sibling.PID == fromPID {
			continue
		}
		if row, err := p.store.GetQualityCache(sibling.PID); err == nil && row != nil && !row.Propagated {
			continue
		}
		_ = p.store.PutQualityCache(&store.QualityCache{
			PID:        sibling.PID,
			ShowName:   sibling.ShowName,
			Heights:    heights,
			ProbedAt:   probedAt,
			Propagated: true,
		})
	}
}

// probeOne runs the full probe for a single item. Returns the heights
// slice on success (possibly empty if BBC has no streams), or nil on
// any error (cached entries are never nil, but the result-map entry
// is nil to signal "probe attempted, no usable answer"). The second
// return value is true only when the playlist was empty
// (ErrNotYetAvailable); that branch is never cached so the next probe
// re-checks once BBC publishes the stream. Issue #44. The third reports
// whether the heights are (now) in the cache, so a show group only hands
// cacheable heights on to its siblings.
func (p *QualityProber) probeOne(parentCtx context.Context, item ProbeItem) (heights []int, notYetAvailable bool, cached bool) {
	// 1. Cache hit short-circuit. A propagated row (probeShowGroup wrote
	// it for a sibling it never probed) is a miss: it holds the show's
	// heights, not this PID's availability. A successful probe below
	// replaces it with a probed row. Gitea #75.
	if row, err := p.store.GetQualityCache(item.PID); err == nil && row != nil && !row.Propagated {
		return row.Heights, false, true
	}

	// 2. Per-probe deadline bounded by the parent context.
	probeCtx, cancel := context.WithTimeout(parentCtx, p.timeout)
	defer cancel()

	// 3. playlist PID -> VPID
	plInfo, err := p.playlist.ResolveCtx(probeCtx, item.PID)
	if err != nil {
		if errors.Is(err, ErrNotYetAvailable) {
			log.Printf("quality probe: not yet available pid=%s (skipping advertise, no cache)", item.PID)
			return nil, true, false
		}
		log.Printf("quality probe failed pid=%s err=%v (playlist)", item.PID, err)
		return nil, false, false
	}
	if plInfo.VPID == "" {
		log.Printf("quality probe failed pid=%s err=no-vpid", item.PID)
		return nil, false, false
	}

	// 4. mediaselector VPID -> streams; walk heights, dedupe, sort descending.
	streams, err := p.ms.ResolveCtx(probeCtx, plInfo.VPID)
	if err != nil {
		log.Printf("quality probe failed pid=%s err=%v (mediaselector)", item.PID, err)
		return nil, false, false
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
	//
	// A 1080 that mediaselector itself reports is verified rather than
	// trusted. On the iptv-all mediaset the single video element's
	// height is the packaging profile, not an encoded rendition, so
	// old programmes (Waterloo Road S05/S06) report 1080 while their
	// HLS master tops out at 720p. Advertising that 1080p sent Sonarr
	// a release whose download came back audio-only. When the claim
	// fails verification the 1080 is swapped for the master's real
	// top height; when it holds, that height is added beneath it so
	// the feed carries a 720p fallback either way. GitHub #54.
	fhdProbeTransientErr := false
	if masters := bestHLSURLs(streams.Video); len(masters) > 0 && len(heights) > 0 && heights[0] >= 720 {
		if containsInt(heights, 1080) {
			has1080, maxListed, err := p.verifyFHDWithFailover(probeCtx, item.PID, masters)
			switch {
			case err != nil:
				log.Printf("quality probe: fhd verify transient err pid=%s err=%v (keeping %v, skipping cache)", item.PID, err, heights)
				fhdProbeTransientErr = true
			case !has1080 && maxListed > 0:
				log.Printf("quality probe: mediaselector reports 1080 for pid=%s but BBC serves at most %dp (no hidden 1080p); advertising %dp", item.PID, maxListed, maxListed)
				heights = replaceHeight(heights, 1080, maxListed)
			case has1080 && maxListed > 0 && maxListed < 1080 && !containsInt(heights, maxListed):
				heights = insertHeight(heights, maxListed)
			}
		} else {
			found, err := p.probeHiddenFHDWithFailover(probeCtx, item.PID, masters)
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
	//
	// Two more answers describe a passing state rather than the
	// programme, and the cache has no TTL (GitHub #57): an
	// accessibility-only version (audio-described is 396p only), and a
	// version mediaselector serves only from live-channel suppliers,
	// which happens for a few minutes after a broadcast, at 720p, before
	// the on-demand files exist.
	skipReason := ""
	switch {
	case fhdProbeTransientErr:
		skipReason = "fhd probe transient error"
	case plInfo.AccessibilityOnly:
		skipReason = "only an audio-described or signed version"
	case liveSuppliersOnly(streams.Video):
		skipReason = "live-channel suppliers only"
	}
	if skipReason == "" {
		if err := p.store.PutQualityCache(&store.QualityCache{
			PID:      item.PID,
			ShowName: item.ShowName,
			Heights:  heights,
			ProbedAt: time.Now(),
		}); err != nil {
			log.Printf("quality probe cache write failed pid=%s err=%v", item.PID, err)
			// Fall through — the result is still usable for this response
			// even if the cache write failed.
		} else {
			cached = true
		}
	} else if !fhdProbeTransientErr {
		log.Printf("quality probe pid=%s: not caching heights %v (%s)", item.PID, heights, skipReason)
	}

	// 7. Log success at INFO level via the ring buffer (any existing logger).
	log.Printf("quality probe pid=%s heights=%v", item.PID, heights)
	return heights, false, cached
}

// liveSuppliersOnly reports whether every stream comes from a
// live-channel supplier (akamai_live_uk, bbc_bidi_live_uk and so on).
func liveSuppliersOnly(video []VideoStream) bool {
	if len(video) == 0 {
		return false
	}
	for _, v := range video {
		if !strings.HasSuffix(v.Supplier, "_live_uk") {
			return false
		}
	}
	return true
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

// bestHLSURLs returns the URL of every HLS stream at the highest bitrate,
// in mediaselector order (parseResponse keeps that order for equal
// streams), without repeats. The first is the URL the FHD check used to
// take alone; the rest are other suppliers of the same rendition, tried
// when it fails. Nil when no HLS stream has a bitrate. Gitea #91.
func bestHLSURLs(streams []VideoStream) []string {
	bestBitrate := 0
	for _, s := range streams {
		if s.Format == "hls" && s.Bitrate > bestBitrate {
			bestBitrate = s.Bitrate
		}
	}
	if bestBitrate == 0 {
		return nil
	}
	var urls []string
	for _, s := range streams {
		if s.Format == "hls" && s.Bitrate == bestBitrate && !slices.Contains(urls, s.URL) {
			urls = append(urls, s.URL)
		}
	}
	return urls
}

// verifyFHDWithFailover runs VerifyFHD on each master URL in turn and
// returns the first answer that is not an error, so one refusing
// supplier no longer leaves a 1080 claim unverified and uncached. Only
// when every supplier errors, or the probe deadline passes, is the last
// error returned. Gitea #91.
func (p *QualityProber) verifyFHDWithFailover(ctx context.Context, pid string, masters []string) (has1080 bool, maxListed int, err error) {
	for i, master := range masters {
		has1080, maxListed, err = p.fhdProber.VerifyFHD(ctx, master)
		if err == nil {
			return has1080, maxListed, nil
		}
		if ctx.Err() != nil {
			break
		}
		if i+1 < len(masters) {
			log.Printf("quality probe: fhd verify failed on supplier %d of %d pid=%s err=%v, trying the next", i+1, len(masters), pid, err)
		}
	}
	return false, 0, err
}

// probeHiddenFHDWithFailover runs ProbeHiddenFHD on each master URL in
// turn and returns the first answer that is not an error. Only when every
// supplier errors, or the probe deadline passes, is the last error
// returned. Gitea #91.
func (p *QualityProber) probeHiddenFHDWithFailover(ctx context.Context, pid string, masters []string) (found bool, err error) {
	for i, master := range masters {
		_, found, err = p.fhdProber.ProbeHiddenFHD(ctx, master)
		if err == nil {
			return found, nil
		}
		if ctx.Err() != nil {
			break
		}
		if i+1 < len(masters) {
			log.Printf("quality probe: hidden fhd probe failed on supplier %d of %d pid=%s err=%v, trying the next", i+1, len(masters), pid, err)
		}
	}
	return false, err
}

// replaceHeight returns heights with every occurrence of old removed and
// with (when non-zero and not already present) inserted, keeping the
// descending order the feed relies on. GitHub #54.
func replaceHeight(heights []int, old, with int) []int {
	out := make([]int, 0, len(heights)+1)
	for _, h := range heights {
		if h != old {
			out = append(out, h)
		}
	}
	if with > 0 {
		out = insertHeight(out, with)
	}
	return out
}

// insertHeight returns heights with h added, deduplicated and sorted
// descending.
func insertHeight(heights []int, h int) []int {
	if containsInt(heights, h) {
		return heights
	}
	out := append(append([]int{}, heights...), h)
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

func containsInt(haystack []int, needle int) bool {
	for _, n := range haystack {
		if n == needle {
			return true
		}
	}
	return false
}
