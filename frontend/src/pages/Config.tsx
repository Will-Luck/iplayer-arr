import { createSignal, onMount, Show, For } from "solid-js";
import type { ConfigResponse } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

export default function Config() {
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
          <label class="text-secondary config-label">Default Quality</label>
          <select class="input" style="width:auto" value={config()!.quality} onChange={e => updateConfig("quality", e.target.value)}>
            <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
          </select>

          <label class="text-secondary config-label">Max Workers</label>
          <div>
            <select class="input" style="width:auto;opacity:0.5" value={config()!.max_workers} disabled aria-disabled="true">
              <option value={config()!.max_workers}>{config()!.max_workers}</option>
            </select>
            <p class="text-muted" style="font-size:11px;margin-top:4px">Set via MAX_WORKERS environment variable</p>
          </div>

          <label class="text-secondary config-label">Download Dir</label>
          <input class="input" type="text" value={config()!.download_dir} disabled aria-disabled="true" style="opacity:0.5" />
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
    </Show>
  );
}
