import { createSignal, For, Show } from "solid-js";
import type { SearchResult } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

export default function Search() {
  const [query, setQuery] = createSignal("");
  const [results, setResults] = createSignal<SearchResult[]>([]);
  const [loading, setLoading] = createSignal(false);
  const [selectedQuality, setSelectedQuality] = createSignal<Record<string, string>>({});

  let debounceTimer: number;

  function onInput(e: InputEvent) {
    const val = (e.target as HTMLInputElement).value;
    setQuery(val);
    clearTimeout(debounceTimer);
    if (val.length < 2) { setResults([]); return; }
    debounceTimer = window.setTimeout(async () => {
      setLoading(true);
      try {
        const res = await api.search(val);
        setResults(res || []);
      } catch (e) { setResults([]); addToast("error", `Search failed: ${e instanceof Error ? e.message : "unknown error"}`); }
      setLoading(false);
    }, 300);
  }

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
    setSelectedQuality(prev => ({ ...prev, [pid]: val }));
  }

  function tierClass(r: SearchResult): string {
    if (r.Series > 0 && r.EpisodeNum > 0) return "badge-completed";
    if (r.Position > 0) return "badge-downloading";
    if (r.AirDate) return "badge-resolving";
    return "badge-pending";
  }

  function tierLabel(r: SearchResult): string {
    if (r.Series > 0 && r.EpisodeNum > 0) return `S${String(r.Series).padStart(2, "0")}E${String(r.EpisodeNum).padStart(2, "0")}`;
    if (r.Position > 0) return `Pos ${r.Position}`;
    if (r.AirDate) return r.AirDate;
    return "Manual";
  }

  return (
    <div>
      <div class="card">
        <div class="card-header">Search BBC iPlayer</div>
        <div class="card-body">
          <input
            class="input"
            type="text"
            placeholder="Search for a programme..."
            value={query()}
            onInput={onInput}
            aria-label="Search BBC iPlayer"
          />
        </div>
      </div>

      <Show when={loading()}>
        <p class="text-muted mt-8 mb-8">Searching...</p>
      </Show>

      <For each={results()}>{r => (
        <div class="card">
          <div class="card-body search-result">
            <Show when={r.Thumbnail}>
              <img src={r.Thumbnail} alt="" class="search-thumb" />
            </Show>
            <div class="search-body">
              <div class="search-title">{r.Title}</div>
              <div class="text-secondary search-subtitle">{r.Subtitle}</div>
              <div class="search-badges">
                <span class={`badge ${tierClass(r)}`}>{tierLabel(r)}</span>
                <Show when={r.Channel}>
                  <span class="badge badge-channel">{r.Channel}</span>
                </Show>
              </div>
              <div class="search-actions">
                <select class="input config-select" value={qualityFor(r.PID)} onChange={e => setQuality(r.PID, e.target.value)} aria-label={`Download quality for ${r.Title}`}>
                  <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
                </select>
                <button class="btn btn-primary btn-sm" onClick={() => startDownload(r)} aria-label={`Download ${r.Title}`}>Download</button>
              </div>
            </div>
          </div>
        </div>
      )}</For>

      <Show when={!loading() && query().length >= 2 && results().length === 0}>
        <div class="card">
          <div class="card-empty">No results found</div>
        </div>
      </Show>
    </div>
  );
}
