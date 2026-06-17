import type { JSX } from "solid-js";
import { createSignal, onMount, Show } from "solid-js";
import type { SystemInfo } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { geoBadge } from "../lib/geo";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { Progress, type ProgressVariant } from "../ui/Progress";
import { Icon } from "../ui/icons";

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

function Row(p: { label: string; children: JSX.Element; mono?: boolean }) {
  return (
    <div class="flex items-start justify-between gap-3 border-b border-border-subtle py-2 last:border-b-0">
      <span class="text-sm text-text-secondary">{p.label}</span>
      <span
        class={`text-right text-sm text-text-primary ${p.mono ? "break-all font-mono text-xs text-text-secondary" : ""}`}
      >
        {p.children}
      </span>
    </div>
  );
}

export default function System() {
  const [info, setInfo] = createSignal<SystemInfo | null>(null);
  const [geoLoading, setGeoLoading] = createSignal(false);
  const sonarrSetup = () => getSonarrSetup(window.location);

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
          ? {
              ...prev,
              geo_ok: result.geo_ok,
              geo_status: result.geo_status,
              geo_detail: result.geo_detail,
              geo_checked_at: result.geo_checked_at,
            }
          : prev,
      );
      const g = geoBadge(result.geo_status, result.geo_ok);
      addToast(
        g.ok ? "success" : "error",
        g.ok
          ? "Geo check passed"
          : `Geo check failed: ${result.geo_detail || g.detail || g.label}`,
      );
    } catch {
      addToast("error", "Geo check request failed");
    } finally {
      setGeoLoading(false);
    }
  }

  return (
    <Show
      when={info()}
      fallback={
        <Card>
          <Card.Body>
            <p class="text-sm text-text-secondary">Loading...</p>
          </Card.Body>
        </Card>
      }
    >
      {(sys) => {
        const diskUsedPct = () =>
          sys().disk_total > 0
            ? Math.round(((sys().disk_total - sys().disk_free) / sys().disk_total) * 100)
            : 0;

        const diskVariant = (): ProgressVariant => {
          const p = diskUsedPct();
          if (p >= 90) return "failed";
          if (p >= 80) return "paused";
          return "default";
        };

        const totalDls = () => sys().downloads_completed + sys().downloads_failed;
        const successRate = () =>
          totalDls() > 0
            ? Math.round((sys().downloads_completed / totalDls()) * 100)
            : 0;

        return (
          <div class="flex flex-col gap-4">
            <h1 class="page-title">System</h1>

            <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <Card>
                <Card.Header>BBC iPlayer status</Card.Header>
                <Card.Body>
                  <Row label="Geo check">
                    {(() => {
                      const g = geoBadge(sys().geo_status, sys().geo_ok);
                      return (
                        <Badge
                          variant={g.ok ? "completed" : "failed"}
                          title={sys().geo_detail || g.detail || undefined}
                        >
                          {g.label}
                        </Badge>
                      );
                    })()}
                  </Row>
                  <Show when={sys().geo_checked_at}>
                    <Row label="Last checked">
                      <span class="text-text-secondary">
                        {new Date(sys().geo_checked_at!).toLocaleString()}
                      </span>
                    </Row>
                  </Show>
                  <div class="mt-3">
                    <Button
                      size="sm"
                      onClick={runGeoCheck}
                      loading={geoLoading()}
                    >
                      <Show when={!geoLoading()}>
                        <Icon name="refresh" size={14} />
                      </Show>
                      {geoLoading() ? "Checking..." : "Re-check"}
                    </Button>
                  </div>
                </Card.Body>
              </Card>

              <Card>
                <Card.Header>ffmpeg</Card.Header>
                <Card.Body>
                  <Row label="Version">
                    <Show
                      when={sys().ffmpeg_version}
                      fallback={<span class="text-danger">Not found</span>}
                    >
                      {sys().ffmpeg_version}
                    </Show>
                  </Row>
                  <Row label="Path" mono>
                    <Show when={sys().ffmpeg_path} fallback="-">
                      {sys().ffmpeg_path}
                    </Show>
                  </Row>
                </Card.Body>
              </Card>

              <Card>
                <Card.Header>Download stats</Card.Header>
                <Card.Body>
                  <Row label="Completed">
                    <span class="tabular">{sys().downloads_completed}</span>
                  </Row>
                  <Row label="Failed">
                    <span class="tabular">{sys().downloads_failed}</span>
                  </Row>
                  <Row label="Success rate">
                    <span class="tabular">{successRate()}%</span>
                  </Row>
                  <Row label="Total downloaded">
                    <span class="tabular">{formatBytes(sys().downloads_total_bytes)}</span>
                  </Row>
                </Card.Body>
              </Card>

              <Card>
                <Card.Header>Storage</Card.Header>
                <Card.Body>
                  <Row label="Download dir" mono>
                    {sys().disk_path || "-"}
                  </Row>
                  <Row label="Free">
                    <span class="tabular">{formatBytes(sys().disk_free)}</span>
                  </Row>
                  <Row label="Total">
                    <span class="tabular">{formatBytes(sys().disk_total)}</span>
                  </Row>
                  <div class="mt-3">
                    <Progress
                      value={diskUsedPct()}
                      variant={diskVariant()}
                      ariaLabel={`Disk usage ${diskUsedPct()}%`}
                    />
                    <p class="mt-1 text-xs text-text-secondary">{diskUsedPct()}% used</p>
                  </div>
                </Card.Body>
              </Card>

              <Card>
                <Card.Header>Sonarr integration</Card.Header>
                <Card.Body>
                  <Row label="Indexer URL" mono>
                    {sonarrSetup().indexerUrl}
                  </Row>
                  <Row label="Last request">
                    <span class="text-text-secondary">
                      <Show when={sys().last_indexer_request} fallback="Never">
                        {new Date(sys().last_indexer_request!).toLocaleString()}
                      </Show>
                    </span>
                  </Row>
                </Card.Body>
              </Card>

              <Card>
                <Card.Header>About</Card.Header>
                <Card.Body>
                  <Row label="Version">{sys().version || "-"}</Row>
                  <Row label="Go version">{sys().go_version || "-"}</Row>
                  <Row label="Uptime">{formatUptime(sys().uptime_seconds)}</Row>
                  <Row label="Build date">
                    <span class="text-text-secondary">{sys().build_date || "-"}</span>
                  </Row>
                </Card.Body>
              </Card>
            </div>
          </div>
        );
      }}
    </Show>
  );
}
