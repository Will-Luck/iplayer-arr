package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/newznab"
)

// Availability verdicts reported per result by /api/search. The strings
// are the wire contract; do not change them without a client note.
const (
	// availabilityAvailable: BBC returned usable stream qualities, so a
	// grab of this PID should download.
	//
	// Best-effort, not a guarantee. probeOne short-circuits on the
	// per-PID quality cache, and probeShowGroup (the newznab feed path)
	// writes cache rows for siblings it never probed. Those rows have no
	// TTL. So for a show Sonarr has already polled, a newly listed
	// episode can read available here while its playlist is unpublished.
	// availabilityNotYetAvailable is trustworthy; this one is a strong
	// hint. Fixing it means changing the feed path's cache propagation
	// and is tracked separately.
	availabilityAvailable = "available"

	// availabilityNotYetAvailable: the episode is in BBC metadata but
	// its playlist is not published. Grabbing it now fails
	// not-yet-available and is deferred by the worker. Issue #52.
	availabilityNotYetAvailable = "not_yet_available"

	// availabilityUnknown: nothing was learned -- no prober wired, the
	// probe timed out, or BBC failed transiently. Deliberately distinct
	// from "available" so a client is never told a guess is a fact.
	availabilityUnknown = "unknown"
)

// defaultSearchProbeTimeout bounds the availability probe so a slow BBC
// cannot hang this UI-facing endpoint. Mirrors the newznab wildcard
// browse ceiling (newznab.browseProbeDeadline). PIDs not answered inside
// the window report unknown.
const defaultSearchProbeTimeout = 5 * time.Second

// maxSearchProbeResults caps how many results are probed per request.
//
// /api/search is reached from a debounced keystroke on the SPA Search
// page, and each probe is up to three BBC round trips (playlist,
// mediaselector, hidden-FHD HEAD). The result count is not naturally
// bounded either: IBL search returns per_page=20, but a brand or series
// hit is expanded through ListEpisodes, so one query can yield hundreds
// of episodes. Without a cap, one keystroke could fan out to hundreds of
// probes against BBC.
//
// Twenty matches the IBL search page size, so an ordinary episode search
// is probed in full and only a large brand expansion is trimmed.
// Results past the cap are still returned, reported as unknown rather
// than dropped: an unmarked result is far better than a missing one.
const maxSearchProbeResults = 20

// searchQualityProber is the slice of bbc.QualityProber that /api/search
// needs. PrefetchPIDsIndividually rather than PrefetchPIDs: the grouped
// call returns one leader's verdict for a whole ShowName, which would
// report an unpublished episode of an otherwise-available show as
// available. See the method doc in internal/bbc/prober.go. Issue #52.
type searchQualityProber interface {
	PrefetchPIDsIndividually(ctx context.Context, items []bbc.ProbeItem) bbc.PrefetchResult
}

// searchResult is an IBL result plus its availability verdict. The IBL
// result is embedded, so encoding/json promotes every existing field to
// the top level and the added key is purely additive: the SPA Search
// page keeps reading the response unchanged.
type searchResult struct {
	bbc.IBLResult
	Availability string
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" || h.ibl == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	writeJSON(w, http.StatusOK, h.markAvailability(r.Context(), results))
}

// markAvailability annotates each result with its availability. Probe
// failures never fail the search: an unmarked result is worth more to a
// caller than no result at all, so every error path degrades to
// availabilityUnknown.
func (h *Handler) markAvailability(ctx context.Context, results []bbc.IBLResult) []searchResult {
	if results == nil {
		// Preserve the existing marshalled shape exactly: a nil slice
		// encodes as null, an empty one as [].
		return nil
	}

	var probe bbc.PrefetchResult
	if h.prober != nil && len(results) > 0 {
		// One ProbeItem per result up to maxSearchProbeResults, each
		// carrying the same show name the newznab feed derives, so a
		// cache row written here keeps the field
		// DeleteQualityCacheByShow reads. Anything past the cap is left
		// unprobed and falls through to unknown.
		probeCount := len(results)
		if probeCount > maxSearchProbeResults {
			probeCount = maxSearchProbeResults
		}
		items := make([]bbc.ProbeItem, 0, probeCount)
		for _, res := range results[:probeCount] {
			items = append(items, bbc.ProbeItem{
				PID:      res.PID,
				ShowName: newznab.IBLResultToProgramme(res).Name,
			})
		}

		timeout := h.searchProbeTimeout
		if timeout <= 0 {
			timeout = defaultSearchProbeTimeout
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		probe = h.prober.PrefetchPIDsIndividually(probeCtx, items)
		cancel()
	}

	out := make([]searchResult, 0, len(results))
	for _, res := range results {
		out = append(out, searchResult{
			IBLResult:    res,
			Availability: availabilityFor(probe, res.PID),
		})
	}
	return out
}

// availabilityFor maps one PID's probe outcome onto a verdict. A PID
// with no usable heights and no not-yet-available flag was either never
// probed or failed transiently; both are unknown, never available.
func availabilityFor(probe bbc.PrefetchResult, pid string) string {
	switch {
	case probe.NotYetAvailable[pid]:
		return availabilityNotYetAvailable
	case len(probe.Heights[pid]) > 0:
		return availabilityAvailable
	default:
		return availabilityUnknown
	}
}
