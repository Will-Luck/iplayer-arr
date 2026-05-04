import { createSignal, onMount, onCleanup, For, Show, createMemo } from "solid-js";
import type { LogEntry } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Select } from "../ui/Select";
import { Icon } from "../ui/icons";

const LEVEL_OPTIONS = [
  { value: "all", label: "All levels" },
  { value: "debug", label: "Debug" },
  { value: "info", label: "Info" },
  { value: "warn", label: "Warn" },
  { value: "error", label: "Error" },
];

const LEVEL_CLASS: Record<string, string> = {
  debug: "text-text-tertiary",
  info: "text-text-secondary",
  warn: "text-warning",
  error: "text-danger",
};

export default function Logs() {
  const [logs, setLogs] = createSignal<LogEntry[]>([]);
  const [levelFilter, setLevelFilter] = createSignal("all");
  const [search, setSearch] = createSignal("");
  const [paused, setPaused] = createSignal(false);
  const [atBottom, setAtBottom] = createSignal(true);

  let logPanel: HTMLDivElement | undefined;

  const filteredLogs = createMemo(() => {
    const level = levelFilter();
    const q = search().toLowerCase();
    return logs().filter((e) => {
      if (level !== "all" && e.level.toLowerCase() !== level) return false;
      if (q && !e.message.toLowerCase().includes(q) && !e.timestamp.toLowerCase().includes(q))
        return false;
      return true;
    });
  });

  function scrollToBottom() {
    if (logPanel) {
      logPanel.scrollTop = logPanel.scrollHeight;
      setAtBottom(true);
    }
  }

  function onScroll() {
    if (!logPanel) return;
    const { scrollTop, scrollHeight, clientHeight } = logPanel;
    setAtBottom(scrollHeight - scrollTop - clientHeight < 40);
  }

  function appendLog(entry: LogEntry) {
    setLogs((prev) => {
      const next = [...prev, entry];
      return next.length > 2000 ? next.slice(next.length - 2000) : next;
    });
    if (atBottom()) {
      requestAnimationFrame(scrollToBottom);
    }
  }

  onMount(async () => {
    try {
      const initial = await api.getLogs();
      setLogs(initial);
      requestAnimationFrame(scrollToBottom);
    } catch {
      // backend may not have logs yet
    }

    const cleanup = connectSSE({
      "log:line": (data) => {
        if (paused()) return;
        appendLog(data as LogEntry);
      },
    });

    onCleanup(cleanup);
  });

  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Logs</h1>

      <Card>
        <Card.Toolbar>
          <Select
            value={levelFilter()}
            onChange={setLevelFilter}
            options={LEVEL_OPTIONS}
            ariaLabel="Filter by log level"
          />
          <label class="relative flex-1 min-w-[160px]">
            <span class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary">
              <Icon name="search" size={14} />
            </span>
            <input
              type="text"
              placeholder="Search..."
              value={search()}
              onInput={(e) => setSearch(e.currentTarget.value)}
              aria-label="Search log messages"
              class="h-9 w-full rounded-md border border-border bg-elevated pl-8 pr-3 text-sm text-text-primary placeholder:text-text-tertiary transition-colors hover:bg-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
            />
          </label>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setLogs([])}
            aria-label="Clear log display"
          >
            Clear
          </Button>
          <Button
            variant={paused() ? "warning" : "secondary"}
            size="sm"
            onClick={() => setPaused((p) => !p)}
            aria-pressed={paused()}
          >
            {paused() ? "Resume" : "Pause"}
          </Button>
        </Card.Toolbar>

        <div class="relative">
          <div
            class="max-h-[60vh] min-h-[20rem] overflow-auto bg-page p-3 font-mono text-xs leading-5"
            ref={logPanel}
            onScroll={onScroll}
            role="log"
            aria-live="polite"
            aria-label="Log output"
          >
            <Show
              when={filteredLogs().length > 0}
              fallback={
                <div class="px-2 py-4 text-text-secondary">No log entries to display.</div>
              }
            >
              <For each={filteredLogs()}>
                {(entry) => {
                  const level = entry.level.toLowerCase();
                  return (
                    <div class={`whitespace-pre-wrap break-words ${LEVEL_CLASS[level] ?? "text-text-secondary"}`}>
                      <span class="text-text-tertiary">[{entry.timestamp}]</span>{" "}
                      <span class="font-semibold uppercase">[{entry.level.toUpperCase()}]</span>{" "}
                      {entry.message}
                    </div>
                  );
                }}
              </For>
            </Show>
          </div>

          <Show when={!atBottom()}>
            <div class="pointer-events-none absolute bottom-3 right-3">
              <Button
                size="sm"
                onClick={scrollToBottom}
                class="pointer-events-auto shadow-lg"
              >
                <Icon name="chevron-down" size={14} />
                Jump to bottom
              </Button>
            </div>
          </Show>
        </div>
      </Card>
    </div>
  );
}
