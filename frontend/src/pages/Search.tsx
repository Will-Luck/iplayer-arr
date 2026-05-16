import { createSignal, onCleanup, For, Show } from "solid-js";
import type { SearchResult } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { Card } from "../ui/Card";
import { Badge, type BadgeVariant } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Select } from "../ui/Select";
import { EmptyState } from "../ui/EmptyState";
import { Icon } from "../ui/icons";

const QUALITY_SELECT_OPTIONS = QUALITY_OPTIONS.map((q) => ({ value: q, label: q }));

type Tier = { variant: BadgeVariant; label: string };

function tierFor(r: SearchResult): Tier {
  if (r.Series > 0 && r.EpisodeNum > 0) {
    return {
      variant: "completed",
      label: `S${String(r.Series).padStart(2, "0")}E${String(r.EpisodeNum).padStart(2, "0")}`,
    };
  }
  if (r.Position > 0) return { variant: "imported", label: `Pos ${r.Position}` };
  if (r.AirDate) return { variant: "pending", label: r.AirDate };
  return { variant: "neutral", label: "Manual" };
}

export default function Search() {
  const [query, setQuery] = createSignal("");
  const [results, setResults] = createSignal<SearchResult[]>([]);
  const [loading, setLoading] = createSignal(false);
  const [selectedQuality, setSelectedQuality] = createSignal<Record<string, string>>({});

  let debounceTimer: number | undefined;
  // activeController tracks the AbortController for the currently
  // in-flight search. A new keystroke aborts the previous request so
  // an older slow query cannot overwrite a fresher result. Audit item
  // 30.
  let activeController: AbortController | null = null;

  function onInput(e: InputEvent) {
    const val = (e.target as HTMLInputElement).value;
    setQuery(val);
    if (debounceTimer !== undefined) clearTimeout(debounceTimer);
    if (val.length < 2) {
      // Cancel any pending search so a partial query result does not
      // land in an empty input.
      activeController?.abort();
      activeController = null;
      setResults([]);
      setLoading(false);
      return;
    }
    debounceTimer = window.setTimeout(async () => {
      activeController?.abort();
      const ctl = new AbortController();
      activeController = ctl;
      setLoading(true);
      try {
        const res = await api.search(val, { signal: ctl.signal });
        if (!ctl.signal.aborted) {
          setResults(res || []);
        }
      } catch (e) {
        if (e instanceof DOMException && (e.name === "AbortError" || e.name === "TimeoutError")) {
          // Superseded by a newer search or hit the request timeout;
          // do not flash a toast or clear results that belong to the
          // newer query already in flight.
          if (e.name === "TimeoutError" && activeController === ctl) {
            addToast("error", "Search timed out");
            setResults([]);
          }
          return;
        }
        if (activeController === ctl) {
          setResults([]);
          addToast("error", `Search failed: ${e instanceof Error ? e.message : "unknown error"}`);
        }
      } finally {
        if (activeController === ctl) {
          activeController = null;
          setLoading(false);
        }
      }
    }, 300);
  }

  onCleanup(() => {
    if (debounceTimer !== undefined) clearTimeout(debounceTimer);
    activeController?.abort();
    activeController = null;
  });

  async function startDownload(r: SearchResult) {
    const quality = selectedQuality()[r.PID] || "720p";
    try {
      await api.manualDownload(r.PID, quality, r.Title, "sonarr");
      addToast("success", `Download queued: ${r.Title}`);
    } catch (e) {
      addToast("error", `Download failed: ${e instanceof Error ? e.message : "unknown error"}`);
    }
  }

  function qualityFor(pid: string) {
    return selectedQuality()[pid] || "720p";
  }

  function setQuality(pid: string, val: string) {
    setSelectedQuality((prev) => ({ ...prev, [pid]: val }));
  }

  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Search</h1>

      <Card>
        <Card.Header>Search BBC iPlayer</Card.Header>
        <Card.Body>
          <label class="relative block">
            <span class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary">
              <Icon name="search" size={16} />
            </span>
            <input
              type="text"
              placeholder="Search for a programme"
              value={query()}
              onInput={onInput}
              aria-label="Search BBC iPlayer"
              class="h-10 w-full rounded-md border border-border bg-elevated pl-9 pr-3 text-sm text-text-primary placeholder:text-text-tertiary transition-colors hover:bg-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
            />
          </label>
        </Card.Body>
      </Card>

      <Show when={loading()}>
        <p class="text-sm text-text-secondary">Searching...</p>
      </Show>

      <For each={results()}>
        {(r) => {
          const tier = tierFor(r);
          return (
            <Card>
              <Card.Body>
                <div class="flex flex-col gap-4 sm:flex-row">
                  <Show when={r.Thumbnail}>
                    <img
                      src={r.Thumbnail}
                      alt=""
                      class="aspect-video w-full max-w-[12rem] flex-none rounded-md border border-border-subtle object-cover sm:w-48"
                    />
                  </Show>
                  <div class="flex min-w-0 flex-1 flex-col gap-2">
                    <div class="text-base font-semibold text-text-primary">{r.Title}</div>
                    <Show when={r.Subtitle}>
                      <div class="text-sm text-text-secondary">{r.Subtitle}</div>
                    </Show>
                    <div class="flex flex-wrap gap-2">
                      <Badge variant={tier.variant}>{tier.label}</Badge>
                      <Show when={r.Channel}>
                        <Badge variant="neutral">{r.Channel}</Badge>
                      </Show>
                    </div>
                    <div class="mt-1 flex flex-wrap items-center gap-2 sm:justify-end">
                      <Select
                        value={qualityFor(r.PID)}
                        onChange={(v) => setQuality(r.PID, v)}
                        options={QUALITY_SELECT_OPTIONS}
                        ariaLabel={`Download quality for ${r.Title}`}
                      />
                      <Button
                        size="sm"
                        onClick={() => startDownload(r)}
                        aria-label={`Download ${r.Title}`}
                      >
                        <Icon name="download" size={14} />
                        Download
                      </Button>
                    </div>
                  </div>
                </div>
              </Card.Body>
            </Card>
          );
        }}
      </For>

      <Show when={!loading() && query().length >= 2 && results().length === 0}>
        <Card>
          <Card.Body padded={false}>
            <EmptyState
              icon="search"
              title="No results found"
              description="Try a shorter query or a different spelling."
            />
          </Card.Body>
        </Card>
      </Show>
    </div>
  );
}
