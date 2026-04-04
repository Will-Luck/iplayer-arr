import type {
  Download,
  StatusResponse,
  SearchResult,
  ShowOverride,
  ConfigResponse,
  DirectoryEntry,
  LogEntry,
  SystemInfo,
  HistoryPage,
  HistoryStats,
} from "./types";

function buildURL(path: string, params?: Record<string, string>): string {
  const url = new URL(path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      url.searchParams.set(k, v);
    }
  }
  return url.toString();
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  params?: Record<string, string>,
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(buildURL(path, params), {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  return res.json() as Promise<T>;
}

async function get<T>(path: string, params?: Record<string, string>): Promise<T> {
  return request<T>("GET", path, undefined, params);
}

async function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>("POST", path, body);
}

async function put<T>(path: string, body: unknown): Promise<T> {
  return request<T>("PUT", path, body);
}

async function del(path: string): Promise<void> {
  await request<unknown>("DELETE", path);
}

export const api = {
  // Status (no auth)
  getStatus: () => get<StatusResponse>("/api/status"),

  // Downloads
  listDownloads: () => get<Download[]>("/api/downloads"),
  manualDownload: (pid: string, quality: string, title: string, category: string) =>
    post<{ id: string }>("/api/download", { pid, quality, title, category }),

  // History
  listHistory: (params?: Record<string, string>) => get<HistoryPage>("/api/history", params),
  getHistoryStats: (since?: string) => get<HistoryStats>("/api/history/stats", since ? { since } : undefined),
  deleteHistory: (id: string) => del(`/api/history/${id}`),
  clearAllHistory: () => del("/api/history"),

  // Config
  getConfig: () => get<ConfigResponse>("/api/config"),
  putConfig: (key: string, value: string) =>
    put<{ status: string }>("/api/config", { key, value }),

  // Overrides
  listOverrides: () => get<ShowOverride[]>("/api/overrides"),
  putOverride: (o: ShowOverride) =>
    put<{ status: string }>(`/api/overrides/${encodeURIComponent(o.show_name)}`, o),
  deleteOverride: (showName: string) =>
    del(`/api/overrides/${encodeURIComponent(showName)}`),

  // Search
  search: (q: string) => get<SearchResult[]>("/api/search", { q }),

  // Directory
  listDirectory: () => get<DirectoryEntry[]>("/api/downloads/directory"),
  deleteDirectoryFolder: (name: string) => del(`/api/downloads/directory/${encodeURIComponent(name)}`),

  // Pause/Resume
  pause: () => post<{ paused: boolean }>("/api/pause", {}),
  resume: () => post<{ paused: boolean }>("/api/resume", {}),

  // Logs
  getLogs: (level?: string, q?: string) =>
    get<LogEntry[]>("/api/logs", {
      ...(level && { level }),
      ...(q && { q }),
    }),

  // System
  getSystem: () => get<SystemInfo>("/api/system"),
  geoCheck: () =>
    post<{ geo_ok: boolean; geo_checked_at: string }>("/api/system/geo-check", {}),
};
