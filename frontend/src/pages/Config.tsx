import { createSignal, onMount, Show, For } from "solid-js";
import type { ConfigResponse } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

export default function Config() {
  const workerOptions = ["1", "2", "3", "5", "10", "15", "20"];
  const [config, setConfig] = createSignal<ConfigResponse | null>(null);
  const [copied, setCopied] = createSignal(false);

  onMount(async () => {
    setConfig(await api.getConfig());
  });

  function copyKey() {
    navigator.clipboard.writeText(config()!.api_key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  async function updateConfig(key: string, value: string) {
    try {
      await api.putConfig(key, value);
      setConfig(await api.getConfig());
      addToast("success", "Setting saved");
    } catch (e) {
      addToast("error", `Failed to save: ${e instanceof Error ? e.message : "unknown error"}`);
    }
  }

  return (
    <Show when={config()} fallback={<p class="text-muted">Loading...</p>}>
      <div class="card">
        <div class="card-header">API Key</div>
        <div class="card-body">
          <div class="api-key-row">
            <code class="api-key-code" aria-label="API key">
              {config()!.api_key}
            </code>
            <button class="btn btn-primary btn-sm" onClick={copyKey}>{copied() ? "Copied!" : "Copy"}</button>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">Settings</div>
        <div class="card-body config-grid">
          <label class="text-secondary config-label" for="cfg-quality">Default Quality</label>
          <select id="cfg-quality" class="input config-select" value={config()!.quality} onChange={e => updateConfig("quality", e.target.value)}>
            <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
          </select>

          <label class="text-secondary config-label" for="cfg-workers">Max Workers</label>
          <div>
            <select id="cfg-workers" class="input config-select" value={config()!.max_workers} onChange={e => updateConfig("max_workers", e.target.value)}>
              <Show when={!workerOptions.includes(config()!.max_workers)}>
                <option value={config()!.max_workers}>{config()!.max_workers}</option>
              </Show>
              <For each={workerOptions}>{workers => <option value={workers}>{workers}</option>}</For>
            </select>
            <p class="text-muted config-hint">Number of concurrent download workers. Changes apply after restart.</p>
          </div>

          <label class="text-secondary config-label" for="cfg-dir">Download Dir</label>
          <input id="cfg-dir" class="input config-disabled" type="text" value={config()!.download_dir} disabled aria-disabled="true" />

          <label class="text-secondary config-label" for="cfg-cleanup">Auto Cleanup</label>
          <div>
            <label class="config-toggle-label">
              <input
                id="cfg-cleanup"
                type="checkbox"
                checked={config()!.auto_cleanup === "true"}
                onChange={e => updateConfig("auto_cleanup", e.target.checked ? "true" : "false")}
              />
              Remove stale download folders
            </label>
            <p class="text-muted config-hint">When enabled, folders with no .mp4 files are cleaned up every 5 minutes</p>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">Sonarr Setup</div>
        <div class="card-body config-instructions">
          <p><strong>1. Add Indexer</strong> (Settings &gt; Indexers &gt; + &gt; Newznab)</p>
          <p class="text-secondary">URL: <code>http://&lt;host&gt;:8191/newznab/api</code></p>
          <p class="text-secondary">API Key: <code>{config()!.api_key}</code></p>
          <p class="mt-12"><strong>2. Add Download Client</strong> (Settings &gt; Download Clients &gt; + &gt; SABnzbd)</p>
          <p class="text-secondary">Host: <code>&lt;host&gt;</code> &nbsp; Port: <code>8191</code> &nbsp; URL Base: <code>/sabnzbd</code></p>
          <p class="text-secondary">API Key: <code>{config()!.api_key}</code></p>
          <p class="text-secondary">Category: <code>sonarr</code></p>
        </div>
      </div>

      <div class="mt-12">
        <button
          class="btn btn-secondary btn-sm"
          onClick={() => window.dispatchEvent(new Event("rerun-wizard"))}
        >
          Re-run Setup Wizard
        </button>
      </div>
    </Show>
  );
}
