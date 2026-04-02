import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import type { Download, StatusResponse } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
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

export default function Dashboard() {
  const [status, setStatus] = createSignal<StatusResponse | null>(null);
  const [active, setActive] = createSignal<Download[]>([]);
  const [queue, setQueue] = createSignal<Download[]>([]);
  const [history, setHistory] = createSignal<Download[]>([]);

  async function loadData() {
    try {
      const [st, downloads, hist] = await Promise.all([
        api.getStatus(),
        api.listDownloads(),
        api.listHistory(),
      ]);
      setStatus(st);
      splitDownloads(downloads);
      setHistory(hist.slice(0, 20));
    } catch {
      // API may not be available yet
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
    // Update in active list
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

  onMount(() => {
    loadData();

    const cleanup = connectSSE({
      "download:progress": (data) => {
        const dl = data as Download;
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
        api.listHistory().then((h) => setHistory(h.slice(0, 20))).catch(() => {});
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
          api.listHistory().then((h) => setHistory(h.slice(0, 20))).catch(() => {});
        }, 3000);
      },
    });

    onCleanup(cleanup);
  });

  return (
    <div>
      <h1 class="page-title">Dashboard</h1>

      {/* Status bar */}
      <Show when={status()}>
        {(st) => (
          <div class="status-bar">
            <div class="status-item">
              <span class="status-dot" classList={{ ok: st().geo_ok, err: !st().geo_ok }} aria-label={st().geo_ok ? "Geo check passed" : "Geo check failed"} />
              {st().geo_ok ? "UK geo OK" : "Geo blocked"}
            </div>
            <div class="status-item">
              <span class="text-secondary">ffmpeg:</span>
              {st().ffmpeg || "not found"}
            </div>
            <div class="status-item">
              <span class="text-secondary">Workers:</span>
              {st().active_workers}
            </div>
            <div class="status-item">
              <span class="text-secondary">Queue:</span>
              {st().queue_depth}
            </div>
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
                  <div class="progress-bar" role="progressbar" aria-valuenow={Math.round(dl.progress)} aria-valuemin={0} aria-valuemax={100} aria-label={`Download progress for ${dl.title || dl.pid}`}>
                    <div
                      class="progress-fill"
                      classList={{ failed: dl.status === "failed" }}
                      style={{ width: `${Math.min(dl.progress, 100)}%` }}
                    />
                  </div>
                  <div class="dl-meta">
                    <span>{dl.progress.toFixed(1)}%</span>
                    <Show when={dl.size > 0}>
                      <span>{formatBytes(dl.downloaded)} / {formatBytes(dl.size)}</span>
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

      {/* Recent history */}
      <div class="card">
        <div class="card-header">Recent History</div>
        <Show when={history().length > 0} fallback={<div class="card-empty">No history yet</div>}>
          <table class="table">
            <thead>
              <tr>
                <th scope="col">Title</th>
                <th scope="col">Quality</th>
                <th scope="col">Status</th>
                <th scope="col">Completed</th>
              </tr>
            </thead>
            <tbody>
              <For each={history()}>
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
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </Show>
      </div>
    </div>
  );
}
