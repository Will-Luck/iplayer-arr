package store

import "time"

type Download struct {
	ID           string    `json:"id"`
	PID          string    `json:"pid"`
	VPID         string    `json:"vpid"`
	Title        string    `json:"title"`
	ShowName     string    `json:"show_name"`
	Season       int       `json:"season"`
	Episode      int       `json:"episode"`
	AirDate      string    `json:"air_date"`
	IdentityTier string    `json:"identity_tier"`
	Quality      string    `json:"quality"`
	Status       string    `json:"status"`
	Category     string    `json:"category"`
	StreamURL    string    `json:"stream_url"`
	OutputDir    string    `json:"output_dir"`
	OutputFile   string    `json:"output_file"`
	Progress     float64   `json:"progress"`
	Size         int64     `json:"size"`
	Downloaded   int64     `json:"downloaded"`
	Duration     int       `json:"duration"`
	Error        string    `json:"error"`
	FailureCode  string    `json:"failure_code"`
	Retryable    bool      `json:"retryable"`
	RetryCount   int       `json:"retry_count"`
	CreatedAt    time.Time `json:"created_at"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
	FileExists   *bool     `json:"file_exists,omitempty"`
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
	CachedAt     time.Time       `json:"cached_at"`
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

	FailCodeGeoBlocked  = "geo_blocked"
	FailCodeExpired     = "expired"
	FailCodeUnavailable = "stream_unavailable"
	FailCodeFFmpeg      = "ffmpeg_error"
	FailCodeTimeout     = "timeout"
	FailCodeUnknown     = "unknown"

	TierFull     = "full"
	TierPosition = "position"
	TierDate     = "date"
	TierManual   = "manual"
)
