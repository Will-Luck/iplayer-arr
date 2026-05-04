import { createSignal, createEffect, onMount, onCleanup, For, Show } from "solid-js";
import type { Download, StatusResponse, SystemInfo, HistoryStats } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";
import { confirmDialog } from "../confirm";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge, type BadgeVariant } from "../ui/Badge";
import { Table } from "../ui/Table";
import { IconButton } from "../ui/IconButton";
import { Progress } from "../ui/Progress";
import { Pagination } from "../ui/Pagination";
import { Select, type SelectOption } from "../ui/Select";
import { EmptyState } from "../ui/EmptyState";

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

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "";
  return new Date(iso).toLocaleString("en-GB", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function statusVariant(dl: Download): BadgeVariant {
  if (dl.status === "completed" && dl.file_exists === false) return "imported";
  if (dl.status === "completed") return "completed";
  if (dl.status === "failed") return "failed";
  if (dl.status === "pending") return "pending";
  return "neutral";
}

function statusLabel(dl: Download): string {
  if (dl.status === "completed" && dl.file_exists === false) return "imported";
  return dl.status;
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

const STATUS_OPTIONS: SelectOption[] = [
  { value: "", label: "All Statuses" },
  { value: "completed", label: "Completed" },
  { value: "failed", label: "Failed" },
];

function HealthPill(props: {
  state: "ok" | "warn" | "err" | "neutral";
  label: string;
}) {
  const dot = () => {
    switch (props.state) {
      case "ok": return "bg-success";
      case "warn": return "bg-warning";
      case "err": return "bg-danger";
      default: return "bg-text-tertiary";
    }
  };
  const text = () => {
    switch (props.state) {
      case "err": return "text-danger";
      case "neutral": return "text-text-secondary";
      default: return "text-text-primary";
    }
  };
  return (
    <span class="inline-flex items-center gap-2 rounded-full border border-border bg-elevated px-3 py-1.5 text-xs">
      <span class={`h-2 w-2 rounded-full ${dot()}`} aria-hidden="true" />
      <span class={text()}>{props.label}</span>
    </span>
  );
}

export default function Dashboard() {
  const [status, setStatus] = createSignal<StatusResponse | null>(null);
  const [system, setSystem] = createSignal<SystemInfo | null>(null);
  const [active, setActive] = createSignal<Download[]>([]);
  const [queue, setQueue] = createSignal<Download[]>([]);
  const [historyItems, setHistoryItems] = createSignal<Download[]>([]);
  const [totalCount, setTotalCount] = createSignal(0);
  const [paused, setPaused] = createSignal(false);
  const [stats, setStats] = createSignal<HistoryStats | null>(null);

  const [statusFilter, setStatusFilter] = createSignal("");
  const [sinceFilter, setSinceFilter] = createSignal("");
  const [sortField, setSortField] = createSignal("completed_at");
  const [sortOrder, setSortOrder] = createSignal<"asc" | "desc">("desc");
  const [currentPage, setCurrentPage] = createSignal(1);
  const perPage = 20;

  const sinceOptions: SelectOption[] = [
    { value: "", label: "All Time" },
    { value: todayISO(), label: "Today" },
    { value: weekAgoISO(), label: "7 Days" },
    { value: monthAgoISO(), label: "30 Days" },
  ];

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
    if (data.status === "pending") {
      setQueue((prev) => {
        const idx = prev.findIndex((d) => d.id === data.id);
        if (idx >= 0) {
          const next = [...prev];
          next[idx] = data;
          return next;
        }
        return [...prev, data];
      });
      setActive((prev) => prev.filter((d) => d.id !== data.id));
      return;
    }
    setActive((prev) => {
      const idx = prev.findIndex((d) => d.id === data.id);
      if (idx >= 0) {
        const next = [...prev];
        next[idx] = data;
        return next;
      }
      return [...prev, data];
    });
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
    const ok = await confirmDialog({
      title: "Clear all history?",
      message: "Delete all history entries? This cannot be undone.",
      confirmLabel: "Delete all",
      danger: true,
    });
    if (!ok) return;
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

  createEffect(() => {
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
        refreshHistory();
      },
      "pause:changed": (data) => {
        const d = data as { paused: boolean };
        setPaused(d.paused);
      },
      "download:failed": (data) => {
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
        setTimeout(() => {
          setActive((prev) => prev.filter((d) => d.id !== dl.id));
          refreshHistory();
        }, 3000);
      },
    });

    onCleanup(cleanup);
  });

  return (
    <div class="flex flex-col gap-5">
      <h1 class="text-2xl font-semibold">Dashboard</h1>

      {/* Health strip */}
      <Show when={status()}>
        {(st) => (
          <div class="flex flex-wrap items-center gap-2">
            <HealthPill
              state={st().geo_ok ? "ok" : "err"}
              label={st().geo_ok ? "UK OK" : "Geo Blocked"}
            />
            <HealthPill
              state={st().ffmpeg ? "ok" : "err"}
              label={st().ffmpeg || "ffmpeg: Not Found"}
            />
            <Show when={system()}>
              {(sys) => (
                <HealthPill
                  state={sys().last_indexer_request ? "ok" : "neutral"}
                  label={
                    sys().last_indexer_request
                      ? `Sonarr · ${relativeTime(sys().last_indexer_request!)}`
                      : "Sonarr · No requests yet"
                  }
                />
              )}
            </Show>
            <HealthPill
              state={
                st().disk_free === 0
                  ? "neutral"
                  : st().disk_free > 1_073_741_824
                    ? "ok"
                    : "err"
              }
              label={st().disk_free > 0 ? `${formatBytes(st().disk_free)} free` : "Disk unknown"}
            />
            <Button
              class="ml-auto"
              size="sm"
              variant={paused() ? "warning" : "secondary"}
              onClick={togglePause}
            >
              {paused() ? "Resume Downloads" : "Pause Downloads"}
            </Button>
          </div>
        )}
      </Show>

      {/* Active downloads */}
      <Card>
        <Card.Header title="Active Downloads" />
        <Card.Body padded={false}>
          <Show
            when={active().length > 0}
            fallback={
              <EmptyState icon="download" description="No active downloads" class="py-8" />
            }
          >
            <div class="max-h-[400px] overflow-y-auto">
              <For each={active()}>
                {(dl) => (
                  <div class="border-b border-border-subtle px-4 py-3 last:border-b-0">
                    <div class="mb-2 flex items-center justify-between gap-3">
                      <span class="truncate text-sm font-medium">
                        {dl.title || dl.pid}
                      </span>
                      <Badge variant={statusVariant(dl)}>{statusLabel(dl)}</Badge>
                    </div>
                    <Progress
                      value={Math.min(dl.progress, 100)}
                      variant={dl.status === "failed" ? "failed" : "default"}
                      ariaLabel={`Download progress for ${dl.title || dl.pid}`}
                      label={`${dl.progress.toFixed(1)}%`}
                      showLabel
                    />
                    <div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-text-secondary">
                      <Show when={speedMap.get(dl.id)?.lastProgress !== undefined}>
                        {(() => {
                          const speed = calcSpeed(dl.id, dl.progress);
                          return speed ? <span class="tabular">{speed}</span> : null;
                        })()}
                      </Show>
                      <span class="tabular">{formatBytes(dl.downloaded)}</span>
                      <Show when={dl.duration > 0}>
                        <span class="tabular">{formatDuration(dl.duration)}</span>
                      </Show>
                      <Show when={dl.error}>
                        <span class="text-danger">{dl.error}</span>
                      </Show>
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </Card.Body>
      </Card>

      {/* Queue */}
      <Show when={queue().length > 0}>
        <Card>
          <Card.Header title={`Queue (${queue().length})`} />
          <Card.Body padded={false}>
            <div class="max-h-[300px] overflow-y-auto">
              <For each={queue()}>
                {(dl) => (
                  <div class="border-b border-border-subtle px-4 py-3 last:border-b-0">
                    <div class="mb-1 flex items-center justify-between gap-3">
                      <span class="truncate text-sm font-medium">
                        {dl.title || dl.pid}
                      </span>
                      <Badge variant="pending">pending</Badge>
                    </div>
                    <div class="flex gap-3 text-xs text-text-secondary">
                      <span>{dl.quality}</span>
                      <span>{dl.category}</span>
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Card.Body>
        </Card>
      </Show>

      {/* History */}
      <Card>
        <Card.Header
          title="History"
          actions={
            <Button variant="danger" size="sm" onClick={clearAllHistory}>
              Clear all
            </Button>
          }
        />
        <Card.Toolbar>
          <Select
            value={statusFilter()}
            onChange={(v) => {
              setStatusFilter(v);
              setCurrentPage(1);
            }}
            options={STATUS_OPTIONS}
            ariaLabel="Filter by status"
          />
          <Select
            value={sinceFilter()}
            onChange={(v) => {
              setSinceFilter(v);
              setCurrentPage(1);
            }}
            options={sinceOptions}
            ariaLabel="Filter by time"
          />
          <Show when={stats()}>
            {(s) => (
              <span class="ml-auto text-xs text-text-secondary">
                <span class="tabular text-text-primary">{s().completed}</span>{" "}
                completed ·{" "}
                <span class="tabular text-text-primary">{s().failed}</span>{" "}
                failed ·{" "}
                <span class="tabular text-text-primary">
                  {formatBytes(s().total_bytes)}
                </span>{" "}
                total
              </span>
            )}
          </Show>
        </Card.Toolbar>
        <Card.Body padded={false}>
          <Show
            when={historyItems().length > 0}
            fallback={
              <EmptyState
                icon="archive"
                title="No history yet"
                description="Completed and failed downloads will appear here."
              />
            }
          >
            <Table
              sortField={sortField()}
              sortOrder={sortOrder()}
              onSort={toggleSort}
              collapse="card"
            >
              <Table.THead>
                <Table.TR>
                  <Table.TH name="title" sortable>
                    Title
                  </Table.TH>
                  <Table.TH align="center" width={70}>
                    Quality
                  </Table.TH>
                  <Table.TH align="center" width={100}>
                    Status
                  </Table.TH>
                  <Table.TH name="completed_at" sortable width={170}>
                    Completed
                  </Table.TH>
                  <Table.TH align="center" width={80}>
                    Size
                  </Table.TH>
                  <Table.TH width={48} />
                </Table.TR>
              </Table.THead>
              <Table.TBody>
                <For each={historyItems()}>
                  {(dl) => (
                    <Table.TR>
                      <Table.TD primary label="Title">
                        <span class="block truncate" title={dl.title || dl.pid}>
                          {dl.title || dl.pid}
                        </span>
                      </Table.TD>
                      <Table.TD align="center" muted tabular label="Quality">
                        {dl.actual_quality || dl.quality}
                      </Table.TD>
                      <Table.TD align="center" label="Status">
                        <Badge variant={statusVariant(dl)}>{statusLabel(dl)}</Badge>
                      </Table.TD>
                      <Table.TD muted tabular label="Completed">
                        {formatDate(dl.completed_at)}
                      </Table.TD>
                      <Table.TD align="center" muted tabular label="Size">
                        <Show when={dl.size > 0}>{formatBytes(dl.size)}</Show>
                      </Table.TD>
                      <Table.TD align="right" label="">
                        <IconButton
                          icon="trash"
                          tone="danger"
                          size="sm"
                          aria-label={`Delete ${dl.title || dl.pid}`}
                          onClick={() => deleteHistoryItem(dl.id)}
                        />
                      </Table.TD>
                    </Table.TR>
                  )}
                </For>
              </Table.TBody>
            </Table>
          </Show>
        </Card.Body>
        <Show when={totalCount() > perPage}>
          <Card.Footer>
            <Pagination
              current={currentPage()}
              total={totalPages()}
              onPageChange={setCurrentPage}
              showing={historyItems().length}
              totalCount={totalCount()}
            />
          </Card.Footer>
        </Show>
      </Card>
    </div>
  );
}
