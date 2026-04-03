import { createSignal, onMount, onCleanup, For, Show, createMemo } from "solid-js";
import type { LogEntry } from "../types";
import { api } from "../api";
import { connectSSE } from "../sse";

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
      // Cap buffer at 2000 entries
      return next.length > 2000 ? next.slice(next.length - 2000) : next;
    });
    if (atBottom()) {
      // Schedule scroll after DOM update
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
    <div>
      <h1 class="page-title">Logs</h1>

      <div class="log-controls">
        <select
          class="input"
          value={levelFilter()}
          onChange={(e) => setLevelFilter(e.target.value)}
          aria-label="Filter by log level"
        >
          <option value="all">All</option>
          <option value="debug">Debug</option>
          <option value="info">Info</option>
          <option value="warn">Warn</option>
          <option value="error">Error</option>
        </select>

        <input
          class="input"
          type="text"
          placeholder="Search..."
          value={search()}
          onInput={(e) => setSearch(e.target.value)}
          aria-label="Search log messages"
          style="flex:1;min-width:160px"
        />

        <button
          class="btn btn-sm"
          onClick={() => setLogs([])}
          aria-label="Clear log display"
        >
          Clear
        </button>

        <button
          class="btn btn-sm"
          onClick={() => setPaused((p) => !p)}
          aria-pressed={paused()}
        >
          {paused() ? "Resume" : "Pause"}
        </button>
      </div>

      <div
        class="log-panel"
        ref={logPanel}
        onScroll={onScroll}
        role="log"
        aria-live="polite"
        aria-label="Log output"
      >
        <Show
          when={filteredLogs().length > 0}
          fallback={<div class="text-secondary" style="padding:8px">No log entries to display.</div>}
        >
          <For each={filteredLogs()}>
            {(entry) => (
              <div class={`log-line log-${entry.level.toLowerCase()}`}>
                [{entry.timestamp}] [{entry.level.toUpperCase()}] {entry.message}
              </div>
            )}
          </For>
        </Show>
      </div>

      <Show when={!atBottom()}>
        <button class="btn btn-sm log-jump" onClick={scrollToBottom}>
          Jump to bottom
        </button>
      </Show>
    </div>
  );
}
