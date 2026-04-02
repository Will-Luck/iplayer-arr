import { createSignal, onMount, Show, For } from "solid-js";
import type { ConfigResponse } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";

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
    await api.putConfig(key, value);
    setConfig(await api.getConfig());
  }

  return (
    <Show when={config()} fallback={<p class="text-muted">Loading...</p>}>
      <div class="card">
        <div class="card-header">API Key</div>
        <div class="card-body">
          <div style="display:flex;gap:8px;align-items:center">
            <code style="background:var(--bg-input);padding:6px 12px;border-radius:var(--radius);flex:1;font-size:13px;border:1px solid var(--border);overflow:hidden;text-overflow:ellipsis">
              {config()!.api_key}
            </code>
            <button class="btn btn-primary btn-sm" onClick={copyKey}>{copied() ? "Copied!" : "Copy"}</button>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">Settings</div>
        <div class="card-body" style="display:grid;grid-template-columns:150px 1fr;gap:12px;align-items:center">
          <label class="text-secondary" style="font-size:13px">Default Quality</label>
          <select class="input" style="width:auto" value={config()!.quality} onChange={e => updateConfig("quality", e.target.value)}>
            <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
          </select>

          <label class="text-secondary" style="font-size:13px">Max Workers</label>
          <div>
            <select class="input" style="width:auto;opacity:0.5" value={config()!.max_workers} disabled>
              <option value={config()!.max_workers}>{config()!.max_workers}</option>
            </select>
            <p class="text-muted" style="font-size:11px;margin-top:4px">Set via MAX_WORKERS environment variable</p>
          </div>

          <label class="text-secondary" style="font-size:13px">Download Dir</label>
          <input class="input" type="text" value={config()!.download_dir} disabled style="opacity:0.5" />
        </div>
      </div>

      <div class="card">
        <div class="card-header">Sonarr Setup</div>
        <div class="card-body" style="font-size:13px;line-height:1.8">
          <p><strong>1. Add Indexer</strong> (Settings &gt; Indexers &gt; + &gt; Newznab)</p>
          <p class="text-secondary">URL: <code>http://&lt;host&gt;:8191/newznab/api</code></p>
          <p class="text-secondary">API Key: <code>{config()!.api_key}</code></p>
          <p style="margin-top:12px"><strong>2. Add Download Client</strong> (Settings &gt; Download Clients &gt; + &gt; SABnzbd)</p>
          <p class="text-secondary">Host: <code>&lt;host&gt;</code> &nbsp; Port: <code>8191</code> &nbsp; URL Base: <code>/sabnzbd</code></p>
          <p class="text-secondary">API Key: <code>{config()!.api_key}</code></p>
          <p class="text-secondary">Category: <code>sonarr</code></p>
        </div>
      </div>
    </Show>
  );
}
