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
import { authHeaders, UNAUTHORIZED_EVENT } from "./apikey";

function buildURL(path: string, params?: Record<string, string>): string {
  const url = new URL(path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      url.searchParams.set(k, v);
    }
  }
  return url.toString();
}

/**
 * Options accepted by every api.* helper. `signal` lets the caller
 * abort an in-flight request (used by Search to drop superseded
 * queries, audit item 30). `timeoutMs` overrides the 30 s default
 * client-side ceiling, which guards against silently hung requests
 * when the server never responds (audit item 32).
 */
export type ApiOptions = {
  signal?: AbortSignal;
  timeoutMs?: number;
};

const DEFAULT_TIMEOUT_MS = 30_000;

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  params?: Record<string, string>,
  options?: ApiOptions,
): Promise<T> {
  // Every API method funnels through here, so this is the one place the
  // credential has to be attached. The only exception is the SSE stream
  // in sse.ts, which cannot set headers and uses ?apikey= instead.
  const headers: Record<string, string> = { ...authHeaders() };
  if (body) {
    headers["Content-Type"] = "application/json";
  }

  // Compose the external signal (if any) with an internal timeout
  // controller so we get cancellation on either trigger. AbortSignal.any
  // would be cleaner but lacks support in the older Chromium/WebKit
  // versions the SPA still targets, so we wire the link manually.
  const controller = new AbortController();
  const external = options?.signal;
  let externalListener: (() => void) | undefined;
  if (external) {
    if (external.aborted) {
      controller.abort(external.reason);
    } else {
      externalListener = () => controller.abort(external.reason);
      external.addEventListener("abort", externalListener, { once: true });
    }
  }
  const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timer = setTimeout(() => controller.abort(new DOMException("request timed out", "TimeoutError")), timeoutMs);

  try {
    const res = await fetch(buildURL(path, params), {
      method,
      headers,
      body: body ? JSON.stringify(body) : undefined,
      signal: controller.signal,
    });
    if (!res.ok) {
      if (res.status === 401) {
        // Tell the app the stored key is missing or stale so it can
        // reopen the setup wizard. Without this an upgrading operator
        // gets an empty dashboard and no route back to entering a key.
        window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
      }
      const err = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(err.error ?? res.statusText);
    }
    return (await res.json()) as T;
  } finally {
    clearTimeout(timer);
    if (external && externalListener) {
      external.removeEventListener("abort", externalListener);
    }
  }
}

async function get<T>(path: string, params?: Record<string, string>, options?: ApiOptions): Promise<T> {
  return request<T>("GET", path, undefined, params, options);
}

async function post<T>(path: string, body: unknown, options?: ApiOptions): Promise<T> {
  return request<T>("POST", path, body, undefined, options);
}

async function put<T>(path: string, body: unknown, options?: ApiOptions): Promise<T> {
  return request<T>("PUT", path, body, undefined, options);
}

async function del(path: string, options?: ApiOptions): Promise<void> {
  await request<unknown>("DELETE", path, undefined, undefined, options);
}

/**
 * Checks a candidate API key against the server before it is stored.
 *
 * Deliberately bypasses request(): it must not dispatch the unauthorized
 * event (the wizard is already open and asking for a key, so reopening
 * it would loop) and it must send the candidate rather than whatever is
 * currently in localStorage. GET /api/config is the probe target because
 * it is authenticated, cheap and read-only.
 */
export async function probeApiKey(key: string): Promise<boolean> {
  const candidate = key.trim();
  if (candidate === "") return false;
  try {
    const res = await fetch(buildURL("/api/config"), {
      method: "GET",
      headers: { Authorization: `Bearer ${candidate}` },
    });
    return res.ok;
  } catch {
    return false;
  }
}

export const api = {
  // Status. Authenticated like everything else below: it reports disk
  // capacity, VPN geo posture and the ffmpeg build. /api/healthz is the
  // only unauthenticated endpoint and the SPA does not call it.
  getStatus: () => get<StatusResponse>("/api/status"),

  // Downloads
  listDownloads: () => get<Download[]>("/api/downloads"),
  manualDownload: (
    pid: string,
    quality: string,
    title: string,
    category: string,
    meta?: { subtitle?: string; series?: number; episodeNum?: number; position?: number; airDate?: string },
  ) => post<{ id: string }>("/api/download", { pid, quality, title, category, ...meta }),
  cancelDownload: (id: string) => del(`/api/downloads/${encodeURIComponent(id)}`),

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

  // Search. `options` lets the caller plumb in an AbortSignal so a
  // superseded query can be cancelled and not race the latest one
  // into the result list. Audit item 30.
  search: (q: string, options?: ApiOptions) =>
    get<SearchResult[]>("/api/search", { q }, options),

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
    post<{ geo_ok: boolean; geo_status?: string; geo_detail?: string; geo_checked_at: string }>(
      "/api/system/geo-check",
      {},
    ),
};
