import { createSignal, createEffect, onMount, onCleanup, For, Show } from "solid-js";
import type { Download, StatusResponse, SystemInfo, HistoryStats } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
}

function formatDuration(seconds: number): string {
  if (!seconds) return "";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function statusBadgeClass(status: string): string {
  return `badge badge-${status}`;
}

function relativeTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

const speedMap = new Map<string, { lastProgress: number; lastTime: number }>();

function calcSpeed(id: string, progress: number): string {
  const now = Date.now();
  const prev = speedMap.get(id);
  if (!prev) {
    speedMap.set(id, { lastProgress: progress, lastTime: now });
    return "";
  }
  const dt = (now - prev.lastTime) / 1000;
  if (dt < 1) return "";
  const dp = progress - prev.lastProgress;
  speedMap.set(id, { lastProgress: progress, lastTime: now });
  if (dp <= 0) return "";
  return `${(dp / dt).toFixed(1)}%/s`;
}

const todayISO = () => new Date().toISOString().split("T")[0];
const weekAgoISO = () => {
  const d = new Date();
  d.setDate(d.getDate() - 7);
  return d.toISOString().split("T")[0];
};
const monthAgoISO = () => {
  const d = new Date();
  d.setDate(d.getDate() - 30);
  return d.toISOString().split("T")[0];
};

export default function Dashboard() {
  const [status, setStatus] = createSignal<StatusResponse | null>(null);
  const [system, setSystem] = createSignal<SystemInfo | null>(null);
  const [active, setActive] = createSignal<Download[]>([]);
  const [queue, setQueue] = createSignal<Download[]>([]);
  const [historyItems, setHistoryItems] = createSignal<Download[]>([]);
  const [totalCount, setTotalCount] = createSignal(0);
  const [paused, setPaused] = createSignal(false);
  const [stats, setStats] = createSignal<HistoryStats | null>(null);

  // History filter/sort/pagination state
  const [statusFilter, setStatusFilter] = createSignal("");
  const [sinceFilter, setSinceFilter] = createSignal("");
  const [sortField, setSortField] = createSignal("completed_at");
  const [sortOrder, setSortOrder] = createSignal("desc");
  const [currentPage, setCurrentPage] = createSignal(1);
  const perPage = 20;

  const totalPages = () => Math.max(1, Math.ceil(totalCount() / perPage));

  function toggleSort(field: string) {
    if (sortField() === field) {
      setSortOrder((o) => (o === "asc" ? "desc" : "asc"));
    } else {
      setSortField(field);
      setSortOrder("desc");
    }
    setCurrentPage(1);
  }

  async function loadData() {
    try {
      const [st, downloads, sys] = await Promise.all([
        api.getStatus(),
        api.listDownloads(),
        api.getSystem(),
      ]);
      setStatus(st);
      setPaused(st.paused);
      splitDownloads(downloads);
      setSystem(sys);
    } catch {
      // API may not be available yet
    }
  }

  async function togglePause() {
    try {
      if (paused()) {
        await api.resume();
        setPaused(false);
      } else {
        await api.pause();
        setPaused(true);
      }
    } catch {
      // silently fail
    }
  }

  function splitDownloads(downloads: Download[]) {
    const act: Download[] = [];
    const q: Download[] = [];
    for (const dl of downloads) {
      if (dl.status === "pending") {
        q.push(dl);
      } else {
        act.push(dl);
      }
    }
    setActive(act);
    setQueue(q);
  }

  function updateDownload(data: Download) {
    setActive((prev) => {
      const idx = prev.findIndex((d) => d.id === data.id);
      if (idx >= 0) {
        const next = [...prev];
        next[idx] = data;
        return next;
      }
      // Might be moving from queue to active
      return [...prev, data];
    });
    // Remove from queue if present
    setQueue((prev) => prev.filter((d) => d.id !== data.id));
  }

  function refreshHistory() {
    const params: Record<string, string> = {
      page: String(currentPage()),
      per_page: String(perPage),
      sort: sortField(),
      order: sortOrder(),
    };
    if (statusFilter()) params.status = statusFilter();
    if (sinceFilter()) params.since = sinceFilter();

    api
      .listHistory(params)
      .then((page) => {
        setHistoryItems(page.items);
        setTotalCount(page.total);
      })
      .catch(() => {});

    api
      .getHistoryStats(sinceFilter() || undefined)
      .then(setStats)
      .catch(() => {});
  }

  async function deleteHistoryItem(id: string) {
    try {
      await api.deleteHistory(id);
      refreshHistory();
    } catch {
      // silently fail
    }
  }

  async function clearAllHistory() {
    if (!confirm("Delete all history entries? This cannot be undone.")) return;
    try {
      await api.clearAllHistory();
    } catch {
      const items = historyItems();
      for (const dl of items) {
        try { await api.deleteHistory(dl.id); } catch { /* continue */ }
      }
    }
    refreshHistory();
  }

  // Re-fetch history whenever filters, sort or page change
  createEffect(() => {
    // Access all reactive dependencies so the effect re-runs on change
    void currentPage();
    void statusFilter();
    void sinceFilter();
    void sortField();
    void sortOrder();
    refreshHistory();
  });

  onMount(() => {
    loadData();

    const cleanup = connectSSE({
      "download:progress": (data) => {
        const dl = data as Download;
        calcSpeed(dl.id, dl.progress);
        setActive((prev) => {
          const idx = prev.findIndex((d) => d.id === dl.id);
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = dl;
            return next;
          }
          return prev;
        });
      },
      "download:status": (data) => {
        updateDownload(data as Download);
      },
      "download:complete": (data) => {
        const dl = data as Download;
        setActive((prev) => prev.filter((d) => d.id !== dl.id));
        // Refresh history to pick up the completed item
        refreshHistory();
      },
      "pause:changed": (data) => {
        const d = data as { paused: boolean };
        setPaused(d.paused);
      },
      "download:failed": (data) => {
        const dl = data as Download;
        // Mark as failed in the active list briefly, then move to history
        setActive((prev) => {
          const idx = prev.findIndex((d) => d.id === dl.id);
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = dl;
            return next;
          }
          return prev;
        });
        // Refresh after a moment so it appears in history
        setTimeout(() => {
          setActive((prev) => prev.filter((d) => d.id !== dl.id));
          refreshHistory();
        }, 3000);
      },
    });

    onCleanup(cleanup);
  });

  return (
    <div>
      <h1 class="page-title">Dashboard</h1>

      {/* Health strip */}
      <Show when={status()}>
        {(st) => (
          <div class="health-strip">
            {/* Geo Check */}
            <div class="health-pill">
              <span
                class="status-dot"
                classList={{ ok: st().geo_ok, err: !st().geo_ok }}
                aria-hidden="true"
              />
              <span style={{ color: st().geo_ok ? "var(--success)" : "var(--danger)" }}>
                {st().geo_ok ? "UK OK" : "Geo Blocked"}
              </span>
            </div>

            {/* ffmpeg */}
            <div class="health-pill">
              <span
                class="status-dot"
                classList={{ ok: !!st().ffmpeg, err: !st().ffmpeg }}
                aria-hidden="true"
              />
              <span style={{ color: st().ffmpeg ? undefined : "var(--danger)" }}>
                {st().ffmpeg ? st().ffmpeg : "Not Found"}
              </span>
            </div>

            {/* Sonarr */}
            <Show when={system()}>
              {(sys) => (
                <div class="health-pill">
                  <span
                    class="status-dot"
                    classList={{ ok: !!sys().last_indexer_request, warn: !sys().last_indexer_request }}
                    aria-hidden="true"
                  />
                  <span style={{ color: sys().last_indexer_request ? undefined : "var(--warning)" }}>
                    {sys().last_indexer_request
                      ? `Connected · ${relativeTime(sys().last_indexer_request!)}`
                      : "No requests yet"}
                  </span>
                </div>
              )}
            </Show>

            {/* Disk Space */}
            <div class="health-pill">
              <span
                class="status-dot"
                classList={{
                  ok: st().disk_free > 1_073_741_824,
                  err: st().disk_free > 0 && st().disk_free <= 1_073_741_824,
                }}
                aria-hidden="true"
              />
              <span style={{ color: st().disk_free <= 1_073_741_824 && st().disk_free > 0 ? "var(--danger)" : undefined }}>
                {st().disk_free > 0 ? `${formatBytes(st().disk_free)} free` : "Disk unknown"}
              </span>
            </div>

            <button
              class="btn btn-sm"
              style={{
                "margin-left": "auto",
                background: paused() ? "var(--warning)" : "var(--muted)",
                color: "white",
              }}
              onClick={togglePause}
            >
              {paused() ? "Resume Downloads" : "Pause Downloads"}
            </button>
          </div>
        )}
      </Show>

      {/* Active downloads */}
      <div class="card">
        <div class="card-header">Active Downloads</div>
        <div class="card-body">
          <Show when={active().length > 0} fallback={<div class="card-empty">No active downloads</div>}>
            <For each={active()}>
              {(dl) => (
                <div class="dl-item">
                  <div class="dl-row">
                    <span class="dl-title">{dl.title || dl.pid}</span>
                    <span class={statusBadgeClass(dl.status)}>{dl.status}</span>
                  </div>
                  <div
                    class="progress-bar"
                    role="progressbar"
                    aria-valuenow={Math.round(dl.progress)}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-label={`Download progress for ${dl.title || dl.pid}`}
                  >
                    <div
                      class="progress-fill"
                      classList={{ failed: dl.status === "failed" }}
                      style={{ width: `${Math.min(dl.progress, 100)}%` }}
                    />
                  </div>
                  <div class="dl-meta">
                    <span>{dl.progress.toFixed(1)}%</span>
                    <Show when={speedMap.get(dl.id)?.lastProgress !== undefined}>
                      {(() => {
                        const speed = calcSpeed(dl.id, dl.progress);
                        return speed ? <span class="text-muted">{speed}</span> : null;
                      })()}
                    </Show>
                    <Show when={dl.size > 0}>
                      <span>
                        {formatBytes(dl.downloaded)} / {formatBytes(dl.size)}
                      </span>
                    </Show>
                    <Show when={dl.duration > 0}>
                      <span>{formatDuration(dl.duration)}</span>
                    </Show>
                    <span class="text-muted">{dl.quality}</span>
                    <Show when={dl.error}>
                      <span class="text-danger">{dl.error}</span>
                    </Show>
                  </div>
                </div>
              )}
            </For>
          </Show>
        </div>
      </div>

      {/* Queue */}
      <Show when={queue().length > 0}>
        <div class="card">
          <div class="card-header">Queue ({queue().length})</div>
          <div class="card-body">
            <For each={queue()}>
              {(dl) => (
                <div class="dl-item">
                  <div class="dl-row">
                    <span class="dl-title">{dl.title || dl.pid}</span>
                    <span class="badge badge-pending">pending</span>
                  </div>
                  <div class="dl-meta">
                    <span class="text-muted">{dl.quality}</span>
                    <span class="text-muted">{dl.category}</span>
                  </div>
                </div>
              )}
            </For>
          </div>
        </div>
      </Show>

      {/* History */}
      <div class="card">
        <div class="card-header" style={{ display: "flex", "align-items": "center", gap: "8px" }}>
          <span>History</span>
          <button class="btn btn-danger btn-sm ml-auto" onClick={clearAllHistory}>
            Clear All
          </button>
        </div>
        <div class="card-body">
          {/* Stats row */}
          <Show when={stats()}>
            {(s) => (
              <div class="history-stats">
                {s().completed} completed / {s().failed} failed / {formatBytes(s().total_bytes)} total
              </div>
            )}
          </Show>

          {/* Filter controls */}
          <div class="history-controls">
            <select
              class="input config-select"
              onChange={(e) => {
                setStatusFilter(e.target.value);
                setCurrentPage(1);
              }}
            >
              <option value="">All Statuses</option>
              <option value="completed">Completed</option>
              <option value="failed">Failed</option>
            </select>
            <select
              class="input config-select"
              onChange={(e) => {
                setSinceFilter(e.target.value);
                setCurrentPage(1);
              }}
            >
              <option value="">All Time</option>
              <option value={todayISO()}>Today</option>
              <option value={weekAgoISO()}>7 Days</option>
              <option value={monthAgoISO()}>30 Days</option>
            </select>
          </div>

          {/* Table */}
          <Show
            when={historyItems().length > 0}
            fallback={<div class="card-empty">No history yet</div>}
          >
            <table class="table">
              <thead>
                <tr>
                  <th
                    scope="col"
                    data-sortable
                    onClick={() => toggleSort("title")}
                  >
                    Title{" "}
                    {sortField() === "title"
                      ? sortOrder() === "asc"
                        ? "▲"
                        : "▼"
                      : ""}
                  </th>
                  <th scope="col">Quality</th>
                  <th scope="col">Status</th>
                  <th
                    scope="col"
                    data-sortable
                    onClick={() => toggleSort("completed_at")}
                  >
                    Completed{" "}
                    {sortField() === "completed_at"
                      ? sortOrder() === "asc"
                        ? "▲"
                        : "▼"
                      : ""}
                  </th>
                  <th scope="col">Size</th>
                  <th scope="col"></th>
                </tr>
              </thead>
              <tbody>
                <For each={historyItems()}>
                  {(dl) => (
                    <tr>
                      <td>{dl.title || dl.pid}</td>
                      <td class="text-muted">{dl.quality}</td>
                      <td>
                        <span class={statusBadgeClass(dl.status)}>{dl.status}</span>
                      </td>
                      <td class="text-secondary">
                        {dl.completed_at ? new Date(dl.completed_at).toLocaleString() : ""}
                      </td>
                      <td class="text-muted">
                        <Show when={dl.size > 0}>{formatBytes(dl.size)}</Show>
                      </td>
                      <td>
                        <button
                          class="btn btn-danger btn-sm"
                          onClick={() => deleteHistoryItem(dl.id)}
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </Show>

          {/* Pagination */}
          <Show when={totalCount() > perPage}>
            <div class="history-pagination">
              <span class="text-secondary">
                Showing {historyItems().length} of {totalCount()}
              </span>
              <button
                class="btn btn-sm"
                disabled={currentPage() <= 1}
                onClick={() => setCurrentPage((p) => p - 1)}
              >
                Prev
              </button>
              <span>
                Page {currentPage()} of {totalPages()}
              </span>
              <button
                class="btn btn-sm"
                disabled={currentPage() >= totalPages()}
                onClick={() => setCurrentPage((p) => p + 1)}
              >
                Next
              </button>
            </div>
          </Show>
        </div>
      </div>
    </div>
  );
}
