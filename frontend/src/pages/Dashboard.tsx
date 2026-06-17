import { createSignal, createEffect, onMount, onCleanup, For, Show } from "solid-js";
import type { Download, StatusResponse, SystemInfo, HistoryStats } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";
import { confirmDialog } from "../confirm";
import { addToast } from "../toast";
import { geoBadge } from "../lib/geo";
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

const STATUS_ALL = "all";
const SINCE_ALL = "all";

const STATUS_OPTIONS: SelectOption[] = [
  { value: STATUS_ALL, label: "All statuses" },
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

  const [statusFilter, setStatusFilter] = createSignal(STATUS_ALL);
  const [sinceFilter, setSinceFilter] = createSignal(SINCE_ALL);
  const [sortField, setSortField] = createSignal("completed_at");
  const [sortOrder, setSortOrder] = createSignal<"asc" | "desc">("desc");
  const [currentPage, setCurrentPage] = createSignal(1);
  const perPage = 20;

  // Tracks downloads with an in-flight cancel request so a fast
  // double-click cannot fire a second confirm dialog or a duplicate
  // API call, and so the cancel button can render in a disabled
  // state while we wait. Audit items 26, 27.
  const [cancelling, setCancelling] = createSignal<Set<string>>(new Set());

  // Generalised in-flight guard for non-cancel mutating actions
  // (togglePause, deleteHistoryItem, clearAllHistory). Keyed by a
  // short action namespace so each button can independently render
  // disabled while its request is outstanding. v1.5.6 audit
  // follow-up: pre-v1.5.6 only the Cancel button had the guard, so
  // a fast double-click on Pause or a Delete-History row would fire
  // two DELETEs.
  const [pendingActions, setPendingActions] = createSignal<Set<string>>(
    new Set(),
  );
  const isPending = (key: string) => pendingActions().has(key);
  const markPending = (key: string) =>
    setPendingActions((prev) => {
      const next = new Set(prev);
      next.add(key);
      return next;
    });
  const unmarkPending = (key: string) =>
    setPendingActions((prev) => {
      const next = new Set(prev);
      next.delete(key);
      return next;
    });

  // Tracks setTimeout handles created by SSE event handlers so
  // onCleanup can clear them on unmount, preventing a deferred
  // setActive after the component has been destroyed. Audit item 34.
  const pendingTimers = new Set<number>();

  const sinceOptions: SelectOption[] = [
    { value: SINCE_ALL, label: "All time" },
    { value: todayISO(), label: "Today" },
    { value: weekAgoISO(), label: "Last 7 days" },
    { value: monthAgoISO(), label: "Last 30 days" },
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
    } catch (e) {
      // Background refresh: log for debug, do not toast on every mount
      // when the server is still starting up. Audit item 29.
      console.error("dashboard: loadData failed", e);
    }
  }

  async function togglePause() {
    if (isPending("pause")) return;
    markPending("pause");
    try {
      await togglePauseInner();
    } finally {
      unmarkPending("pause");
    }
  }

  async function togglePauseInner() {
    try {
      if (paused()) {
        await api.resume();
        setPaused(false);
      } else {
        await api.pause();
        setPaused(true);
      }
    } catch (e) {
      // User-initiated action: surface the failure so the user knows
      // the button click did not take effect. Audit item 29.
      addToast(
        "error",
        e instanceof Error ? e.message : "Failed to toggle pause state",
      );
    }
  }

  async function cancelDownload(dl: Download) {
    // Guard against a fast double-click reopening the confirm dialog
    // or firing a duplicate DELETE. Audit item 26.
    if (cancelling().has(dl.id)) return;
    const ok = await confirmDialog({
      title: "Cancel download?",
      message: `Stop downloading "${dl.title || dl.pid}"? Any partial file will be cleaned up.`,
      confirmLabel: "Cancel download",
      cancelLabel: "Keep",
      danger: true,
    });
    if (!ok) return;

    // Mark as cancelling so the button renders disabled and a second
    // click is rejected at the guard above. Audit items 26, 27.
    setCancelling((prev) => {
      const next = new Set(prev);
      next.add(dl.id);
      return next;
    });

    // Optimistic removal so the row visibly disappears the instant the
    // user confirms. If the API call fails we restore canonical state
    // by reloading from the server (cheaper than threading the
    // pre-removal snapshots back through every setter). Audit item 27.
    setActive((prev) => prev.filter((d) => d.id !== dl.id));
    setQueue((prev) => prev.filter((d) => d.id !== dl.id));

    try {
      await api.cancelDownload(dl.id);
      // Drop the speed sample for this id so the map does not leak
      // entries for completed/cancelled downloads over a long session.
      // Audit item 33.
      speedMap.delete(dl.id);
      addToast("success", `Cancelled ${dl.title || dl.pid}`);
    } catch (e) {
      addToast(
        "error",
        e instanceof Error ? e.message : "Failed to cancel download",
      );
      // Revert the optimistic removal by refetching the canonical
      // downloads list; the row will reappear if the server still has
      // it. Audit item 27.
      loadData();
    } finally {
      setCancelling((prev) => {
        const next = new Set(prev);
        next.delete(dl.id);
        return next;
      });
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
    if (statusFilter() !== STATUS_ALL) params.status = statusFilter();
    if (sinceFilter() !== SINCE_ALL) params.since = sinceFilter();

    api
      .listHistory(params)
      .then((page) => {
        setHistoryItems(page.items);
        setTotalCount(page.total);
      })
      .catch((e) => {
        // Background refresh: log for debug only. The history card has
        // an empty-state fallback that renders fine on stale data, so a
        // toast here would be noise. Audit item 29.
        console.error("dashboard: listHistory failed", e);
      });

    api
      .getHistoryStats(sinceFilter() === SINCE_ALL ? undefined : sinceFilter())
      .then(setStats)
      .catch((e) => {
        console.error("dashboard: getHistoryStats failed", e);
      });
  }

  async function deleteHistoryItem(id: string) {
    const key = "history:" + id;
    if (isPending(key)) return;
    markPending(key);
    try {
      await api.deleteHistory(id);
      refreshHistory();
    } catch (e) {
      // User-initiated row delete: surface the failure. Audit item 29.
      addToast(
        "error",
        e instanceof Error ? e.message : "Failed to delete history entry",
      );
    } finally {
      unmarkPending(key);
    }
  }

  async function clearAllHistory() {
    if (isPending("clear-all")) return;
    const ok = await confirmDialog({
      title: "Clear all history?",
      message: "Delete all history entries? This cannot be undone.",
      confirmLabel: "Delete all",
      danger: true,
    });
    if (!ok) return;
    markPending("clear-all");
    try {
      // The clear-all endpoint is the only correct path: the previous
      // per-row fallback only deleted the visible page, producing a
      // misleading "succeeded" experience while leaving older history
      // intact. Audit item 31.
      await api.clearAllHistory();
      refreshHistory();
    } catch (e) {
      addToast(
        "error",
        e instanceof Error ? e.message : "Failed to clear history",
      );
    } finally {
      unmarkPending("clear-all");
    }
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
        // Drop the per-id speed sample so the map stays bounded
        // across long sessions. Audit item 33.
        speedMap.delete(dl.id);
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
        // Track the setTimeout handle so onCleanup can clear it if the
        // user navigates away during the 3 s grace window. Without
        // this, the deferred setActive runs on an unmounted component.
        // Audit item 34. Also drops the speedMap entry on fire (audit
        // item 33) so failed-then-removed downloads do not leak.
        const t = window.setTimeout(() => {
          pendingTimers.delete(t);
          setActive((prev) => prev.filter((d) => d.id !== dl.id));
          speedMap.delete(dl.id);
          refreshHistory();
        }, 3000);
        pendingTimers.add(t);
      },
    });

    onCleanup(() => {
      cleanup();
      // Cancel any deferred download:failed handlers still in flight.
      // Audit item 34.
      for (const t of pendingTimers) {
        clearTimeout(t);
      }
      pendingTimers.clear();
    });
  });

  return (
    <div class="flex flex-col gap-5">
      <h1 class="text-2xl font-semibold">Dashboard</h1>

      {/* Health strip */}
      <Show when={status()}>
        {(st) => (
          <div class="flex flex-wrap items-center gap-2">
            {(() => {
              const g = geoBadge(st().geo_status, st().geo_ok);
              return <HealthPill state={g.ok ? "ok" : "err"} label={g.label} />;
            })()}
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
              disabled={isPending("pause")}
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
                      <div class="flex shrink-0 items-center gap-2">
                        <Badge variant={statusVariant(dl)}>{statusLabel(dl)}</Badge>
                        <IconButton
                          icon="cross"
                          tone="danger"
                          size="sm"
                          aria-label={`Cancel ${dl.title || dl.pid}`}
                          title={cancelling().has(dl.id) ? "Cancelling..." : "Cancel download"}
                          disabled={cancelling().has(dl.id)}
                          onClick={() => cancelDownload(dl)}
                        />
                      </div>
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
                      <div class="flex shrink-0 items-center gap-2">
                        <Badge variant="pending">pending</Badge>
                        <IconButton
                          icon="cross"
                          tone="danger"
                          size="sm"
                          aria-label={`Cancel ${dl.title || dl.pid}`}
                          title={cancelling().has(dl.id) ? "Cancelling..." : "Cancel download"}
                          disabled={cancelling().has(dl.id)}
                          onClick={() => cancelDownload(dl)}
                        />
                      </div>
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
            <Button
              variant="danger"
              size="sm"
              onClick={clearAllHistory}
              disabled={isPending("clear-all")}
            >
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
                          disabled={isPending("history:" + dl.id)}
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
