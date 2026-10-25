package store

import "time"

type Download struct {
	ID            string    `json:"id"`
	PID           string    `json:"pid"`
	VPID          string    `json:"vpid"`
	Title         string    `json:"title"`
	ShowName      string    `json:"show_name"`
	Season        int       `json:"season"`
	Episode       int       `json:"episode"`
	AirDate       string    `json:"air_date"`
	IdentityTier  string    `json:"identity_tier"`
	Quality       string    `json:"quality"`
	ActualQuality string    `json:"actual_quality,omitempty"`
	Status        string    `json:"status"`
	Category      string    `json:"category"`
	StreamURL     string    `json:"stream_url"`
	OutputDir     string    `json:"output_dir"`
	OutputFile    string    `json:"output_file"`
	Progress      float64   `json:"progress"`
	Size          int64     `json:"size"`
	Downloaded    int64     `json:"downloaded"`
	Duration      int       `json:"duration"`
	Error         string    `json:"error"`
	FailureCode   string    `json:"failure_code"`
	Retryable     bool      `json:"retryable"`
	RetryCount    int       `json:"retry_count"`
	RetryAfter    time.Time `json:"retry_after"`
	// CDNFailures counts CDN-style failures (ffmpeg_error / truncated)
	// only, separate from the total RetryCount, so quality-degrade-on-retry
	// steps the ladder by genuine stream failures. The counter is never
	// reset: it is preserved across stalled and not-yet-available retries
	// rather than counting them. #46, #56.
	CDNFailures int `json:"cdn_failures"`
	// StallCount is the download's dedicated watchdog-stall budget and
	// escalation driver, kept separate from the shared RetryCount so a
	// stall neither consumes the CDN/not-yet-available retry budget nor is
	// diluted by it. It gates stall permanence (StallCount < maxStalls) and
	// widens the watchdog window on each retry. GitHub #50.
	StallCount int `json:"stall_count"`
	// StallCredits counts stalls that were frozen (net-zeroed against
	// StallCount) while an adaptive-throttle cooldown was active, so a
	// synchronised season-grab cluster cannot silently burn its stall budget
	// and be lost. Bounded by maxStallCredits so a genuinely dead stream
	// still reaches permanence and is handed to Sonarr for re-grab. GitHub #50.
	StallCredits int `json:"stall_credits"`
	// MetadataFailures counts metadata_unavailable failures, the
	// download's own budget for "the BBC could not tell us about this
	// episode" (an HTML error page, an unexpected iBL response, a timeout),
	// kept apart from RetryCount like StallCount so not-yet-available
	// attempts cannot use it up. GitHub #57.
	MetadataFailures int `json:"metadata_failures"`
	// Supplier is the CDN supplier (mediaselector's supplier attribute,
	// e.g. mf_akamai) of the stream picked for the latest attempt, kept
	// alongside StreamURL. GitHub #55.
	Supplier string `json:"supplier,omitempty"`
	// FailedSuppliers lists the suppliers that refused an attempt, oldest
	// first: the HLS master playlist answered non-2xx or was unreachable,
	// or ffmpeg hit an HTTP 4xx, a refused connection or a DNS failure.
	// The next attempt prefers another supplier at the same height, and a
	// refusal does not bump CDNFailures, so it never degrades quality.
	// Never reset, like CDNFailures. GitHub #55.
	FailedSuppliers []string `json:"failed_suppliers,omitempty"`
	// Priority orders the pending queue ahead of every other key: a
	// higher value is claimed sooner. Zero is the default for every
	// arrival, so untouched queues keep the show/season/episode order.
	// MoveDownload renumbers the pending rows when the operator (or a
	// SABnzbd priority from Sonarr) repositions one. GitHub #51.
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	FileExists  *bool     `json:"file_exists,omitempty"`
}

type Programme struct {
	PID          string          `json:"pid"`
	VPID         string          `json:"vpid"`
	Name         string          `json:"name"`
	Episode      string          `json:"episode"`
	Series       int             `json:"series"`
	EpisodeNum   int             `json:"episode_num"`
	AirDate      string          `json:"air_date"`
	Channel      string          `json:"channel"`
	Duration     int             `json:"duration"`
	Synopsis     string          `json:"synopsis"`
	Thumbnail    string          `json:"thumbnail"`
	Available    time.Time       `json:"available"`
	Expires      time.Time       `json:"expires"`
	Qualities    []QualityOption `json:"qualities"`
	Position     int             `json:"position"`
	IdentityTier string          `json:"identity_tier"`
	IsDateBased  bool            `json:"is_date_based"`
	// TVDBNumbered marks Series and EpisodeNum as TheTVDB's numbers,
	// which GenerateTitle emits without applying the numbering
	// overrides. GitHub #60.
	TVDBNumbered bool `json:"tvdb_numbered,omitempty"`
	// SeriesAssumed marks Series as the Series 1 iplayer-arr assumes
	// for an episode number with no series (GitHub #32), not BBC's.
	SeriesAssumed bool `json:"series_assumed,omitempty"`
	// EpisodeAssumed marks EpisodeNum as the air-date order iplayer-arr
	// gives a series BBC numbers no episode of, not BBC's. Gitea #104.
	EpisodeAssumed bool `json:"episode_assumed,omitempty"`
	// TVDBUnchecked marks a programme whose only number is a guess from
	// its iPlayer position, when the TheTVDB list that would check it is
	// unavailable. Gitea #104.
	TVDBUnchecked bool `json:"tvdb_unchecked,omitempty"`
	// TVDBContradicted marks a programme whose number is a guess or partly
	// assumed, or belongs to another show of the name, where TheTVDB has
	// another episode at that number aired more than a week away. GitHub
	// #60, Gitea #104.
	TVDBContradicted bool      `json:"tvdb_contradicted,omitempty"`
	CachedAt         time.Time `json:"cached_at"`
}

type QualityOption struct {
	Tag       string `json:"tag"`
	Height    int    `json:"height"`
	Bitrate   int    `json:"bitrate"`
	FPS       int    `json:"fps"`
	StreamURL string `json:"stream_url"`
	Supplier  string `json:"supplier"`
	HasSubs   bool   `json:"has_subs"`
	Synthetic bool   `json:"synthetic"`
}

type SeriesMapping struct {
	TVDBId    string    `json:"tvdb_id"`
	ShowName  string    `json:"show_name"`
	Year      int       `json:"year"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ShowOverride struct {
	ShowName       string `json:"show_name"`
	ForceDateBased bool   `json:"force_date_based"`
	ForceSeriesNum int    `json:"force_series_num"`
	ForcePosition  bool   `json:"force_position"`
	SeriesOffset   int    `json:"series_offset"`
	EpisodeOffset  int    `json:"episode_offset"`
	CustomName     string `json:"custom_name"`
}

// QualityCache records the heights BBC actually offers for a single PID.
// Populated by bbc.QualityProber on first encounter and reused
// indefinitely (BBC content masters are append-only once published, so
// cache entries never need TTL-based invalidation). Manual refresh is
// handled by the DeleteQualityCache / DeleteQualityCacheByShow methods
// on *store.Store.
//
// ShowName is stored in normalised form (lower-cased and trimmed via
// the existing normaliseShowName helper at overrides.go:10) so that
// DeleteQualityCacheByShow matches cache entries regardless of how the
// user typed the show name in the future v1.2 refresh-button UI. The
// original casing is never needed because the cache is consulted by
// PID, not by show name.
type QualityCache struct {
	PID      string    `json:"pid"`
	ShowName string    `json:"show_name"` // normalised: lowercase + trimmed
	Heights  []int     `json:"heights"`   // sorted descending, e.g. [1080, 720, 540, 396]
	ProbedAt time.Time `json:"probed_at"`
}

const (
	StatusPending     = "pending"
	StatusResolving   = "resolving"
	StatusDownloading = "downloading"
	StatusConverting  = "converting"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"

	FailCodeGeoBlocked      = "geo_blocked"
	FailCodeExpired         = "expired"
	FailCodeUnavailable     = "stream_unavailable"
	FailCodeNotYetAvailable = "not_yet_available"
	FailCodeFFmpeg          = "ffmpeg_error"
	FailCodeTruncated       = "truncated"
	FailCodeStalled         = "stalled"
	FailCodeTimeout         = "timeout"
	FailCodeUnknown         = "unknown"

	// FailCodeMetadataUnavailable: the BBC's programme metadata could not
	// be fetched or understood. Says nothing about whether the episode is
	// downloadable, so a later grab supersedes it. GitHub #57.
	FailCodeMetadataUnavailable = "metadata_unavailable"

	TierFull     = "full"
	TierPosition = "position"
	TierDate     = "date"
	TierManual   = "manual"
)
