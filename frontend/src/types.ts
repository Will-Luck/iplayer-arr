export interface Download {
  id: string;
  pid: string;
  vpid: string;
  title: string;
  show_name: string;
  season: number;
  episode: number;
  air_date: string;
  identity_tier: string;
  quality: string;
  actual_quality?: string;
  status: string;
  category: string;
  stream_url: string;
  output_dir: string;
  output_file: string;
  progress: number;
  size: number;
  downloaded: number;
  duration: number;
  error: string;
  failure_code: string;
  retryable: boolean;
  retry_count: number;
  created_at: string;
  started_at: string;
  completed_at: string;
  file_exists?: boolean;
}

export interface StatusResponse {
  ffmpeg: string;
  geo_ok: boolean;
  geo_status?: string;
  geo_detail?: string;
  active_workers: number;
  queue_depth: number;
  paused: boolean;
  disk_free: number;
  disk_total: number;
}

export interface SearchResult {
  PID: string;
  Title: string;
  Subtitle: string;
  Synopsis: string;
  Channel: string;
  Series: number;
  EpisodeNum: number;
  Position: number;
  AirDate: string;
  Thumbnail: string;
  BrandPID: string;
  // Availability tells whether BBC has actually published this
  // episode's playlist. "not_yet_available" means a grab would fail and
  // be deferred; "unknown" means the probe could not answer. Optional:
  // older servers omit it. Issue #52.
  Availability?: "available" | "not_yet_available" | "unknown";
}

export interface ShowOverride {
  show_name: string;
  force_date_based: boolean;
  force_series_num: number;
  force_position: boolean;
  series_offset: number;
  episode_offset: number;
  custom_name: string;
}

// GET /api/config deliberately does not return api_key. The endpoint was
// unauthenticated until GHSA-3hfw-5v8p-p588 and handed the secret to any
// caller; the field is gone rather than merely gated. The SPA keeps the
// key in localStorage (see apikey.ts) and the operator obtains it from
// <CONFIG_DIR>/api_key or the API_KEY environment variable.
export interface ConfigResponse {
  quality: string;
  max_workers: string;
  download_dir: string;
  auto_cleanup: string;
}

export interface DirectoryFile {
  name: string;
  size: number;
}

export interface DirectoryEntry {
  name: string;
  path: string;
  files: DirectoryFile[];
  total_size: number;
  owned: boolean;
}

export const QUALITY_OPTIONS = ["1080p", "720p", "540p", "396p"] as const;

// QUALITY_CEILING_OPTIONS is the dropdown used by the Maximum quality
// config setting. "any" means no ceiling (Sonarr can request whatever
// the BBC delivers); the named heights cap what the indexer advertises.
export const QUALITY_CEILING_OPTIONS = ["any", "1080p", "720p", "540p", "396p"] as const;

export interface HistoryPage {
  items: Download[];
  total: number;
}

export interface HistoryStats {
  completed: number;
  failed: number;
  total_bytes: number;
}

export interface LogEntry {
  timestamp: string;
  level: string;
  message: string;
}

export interface SystemInfo {
  version: string;
  go_version: string;
  uptime_seconds: number;
  build_date: string;
  geo_ok: boolean;
  geo_status?: string;
  geo_detail?: string;
  geo_checked_at?: string;
  ffmpeg_version: string;
  ffmpeg_path: string;
  disk_total: number;
  disk_free: number;
  disk_path: string;
  downloads_completed: number;
  downloads_failed: number;
  downloads_total_bytes: number;
  last_indexer_request?: string;
}
