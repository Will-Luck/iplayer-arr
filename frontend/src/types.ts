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
}

export interface StatusResponse {
  ffmpeg: string;
  geo_ok: boolean;
  active_workers: number;
  queue_depth: number;
  paused: boolean;
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

export interface ConfigResponse {
  api_key: string;
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
