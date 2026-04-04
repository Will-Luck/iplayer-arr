import { createSignal, onMount, Show } from "solid-js";
import type { SystemInfo } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(" ");
}

export default function System() {
  const [info, setInfo] = createSignal<SystemInfo | null>(null);
  const [geoLoading, setGeoLoading] = createSignal(false);

  onMount(async () => {
    try {
      setInfo(await api.getSystem());
    } catch {
      addToast("error", "Failed to load system info");
    }
  });

  async function runGeoCheck() {
    setGeoLoading(true);
    try {
      const result = await api.geoCheck();
      setInfo((prev) =>
        prev
          ? { ...prev, geo_ok: result.geo_ok, geo_checked_at: result.geo_checked_at }
          : prev,
      );
      addToast(result.geo_ok ? "success" : "error", result.geo_ok ? "Geo check passed" : "Geo check failed");
    } catch {
      addToast("error", "Geo check request failed");
    } finally {
      setGeoLoading(false);
    }
  }

  return (
    <Show when={info()} fallback={<p class="text-muted">Loading...</p>}>
      {(sys) => {
        const diskUsedPct = () =>
          sys().disk_total > 0
            ? Math.round(((sys().disk_total - sys().disk_free) / sys().disk_total) * 100)
            : 0;

        const totalDls = () => sys().downloads_completed + sys().downloads_failed;
        const successRate = () =>
          totalDls() > 0
            ? Math.round((sys().downloads_completed / totalDls()) * 100)
            : 0;

        return (
          <div>
            <h1 class="page-title">System</h1>

            <div class="system-grid">
              {/* BBC iPlayer Status */}
              <div class="card">
                <div class="card-header">BBC iPlayer Status</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Geo check</span>
                    <span class="system-value">
                      <span
                        class="status-dot"
                        classList={{ ok: sys().geo_ok, err: !sys().geo_ok }}
                        aria-label={sys().geo_ok ? "Passed" : "Failed"}
                      />
                      {sys().geo_ok ? "OK — UK access confirmed" : "Blocked"}
                    </span>
                  </div>
                  <Show when={sys().geo_checked_at}>
                    <div class="system-row">
                      <span class="system-label">Last checked</span>
                      <span class="system-value text-secondary">
                        {new Date(sys().geo_checked_at!).toLocaleString()}
                      </span>
                    </div>
                  </Show>
                  <div style="margin-top:12px">
                    <button
                      class="btn btn-sm btn-primary"
                      onClick={runGeoCheck}
                      disabled={geoLoading()}
                    >
                      {geoLoading() ? "Checking..." : "Re-check"}
                    </button>
                  </div>
                </div>
              </div>

              {/* ffmpeg */}
              <div class="card">
                <div class="card-header">ffmpeg</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Version</span>
                    <span class="system-value">
                      <Show when={sys().ffmpeg_version} fallback={<span class="text-danger">Not found</span>}>
                        {sys().ffmpeg_version}
                      </Show>
                    </span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Path</span>
                    <span class="system-value text-secondary" style="word-break:break-all;font-size:12px">
                      <Show when={sys().ffmpeg_path} fallback="—">
                        {sys().ffmpeg_path}
                      </Show>
                    </span>
                  </div>
                </div>
              </div>

              {/* Download Stats */}
              <div class="card">
                <div class="card-header">Download Stats</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Completed</span>
                    <span class="system-value">{sys().downloads_completed}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Failed</span>
                    <span class="system-value">{sys().downloads_failed}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Success rate</span>
                    <span class="system-value">{successRate()}%</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Total downloaded</span>
                    <span class="system-value">{formatBytes(sys().downloads_total_bytes)}</span>
                  </div>
                </div>
              </div>

              {/* Storage */}
              <div class="card">
                <div class="card-header">Storage</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Download dir</span>
                    <span class="system-value text-secondary" style="word-break:break-all;font-size:12px">
                      {sys().disk_path || "—"}
                    </span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Free</span>
                    <span class="system-value">{formatBytes(sys().disk_free)}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Total</span>
                    <span class="system-value">{formatBytes(sys().disk_total)}</span>
                  </div>
                  <div style="margin-top:10px">
                    <div
                      class="progress-bar"
                      role="progressbar"
                      aria-valuenow={diskUsedPct()}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-label={`Disk usage ${diskUsedPct()}%`}
                    >
                      <div class="progress-fill" style={{ width: `${diskUsedPct()}%` }} />
                    </div>
                    <p class="text-secondary" style="font-size:12px;margin-top:4px">{diskUsedPct()}% used</p>
                  </div>
                </div>
              </div>

              {/* Sonarr Integration */}
              <div class="card">
                <div class="card-header">Sonarr Integration</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Indexer URL</span>
                    <span class="system-value text-secondary" style="word-break:break-all;font-size:12px">
                      http://&lt;host&gt;:&lt;port&gt;/newznab/api
                    </span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Last request</span>
                    <span class="system-value text-secondary">
                      <Show when={sys().last_indexer_request} fallback="Never">
                        {new Date(sys().last_indexer_request!).toLocaleString()}
                      </Show>
                    </span>
                  </div>
                </div>
              </div>

              {/* About */}
              <div class="card">
                <div class="card-header">About</div>
                <div class="card-body">
                  <div class="system-row">
                    <span class="system-label">Version</span>
                    <span class="system-value">{sys().version || "—"}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Go version</span>
                    <span class="system-value">{sys().go_version || "—"}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Uptime</span>
                    <span class="system-value">{formatUptime(sys().uptime_seconds)}</span>
                  </div>
                  <div class="system-row">
                    <span class="system-label">Build date</span>
                    <span class="system-value text-secondary">{sys().build_date || "—"}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        );
      }}
    </Show>
  );
}
