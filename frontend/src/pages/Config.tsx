import { createSignal, onMount, Show, For } from "solid-js";
import type { ConfigResponse } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { copyToClipboard } from "../lib/clipboard";

export default function Config() {
  const workerOptions = ["1", "2", "3", "5", "10", "15", "20"];
  const [config, setConfig] = createSignal<ConfigResponse | null>(null);
  const [copiedField, setCopiedField] = createSignal<string | null>(null);
  const [keyRevealed, setKeyRevealed] = createSignal(false);
  const sonarrSetup = () => getSonarrSetup(window.location);

  onMount(async () => {
    setConfig(await api.getConfig());
  });

  async function copyField(value: string, key: string) {
    const ok = await copyToClipboard(value);
    if (!ok) return;
    setCopiedField(key);
    setTimeout(() => setCopiedField(null), 2000);
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
              {keyRevealed() ? config()!.api_key : config()!.api_key.slice(0, 4) + "••••••••" + config()!.api_key.slice(-4)}
            </code>
            <button class="btn btn-sm" onClick={() => setKeyRevealed(!keyRevealed())} title={keyRevealed() ? "Hide" : "Reveal"}>{keyRevealed() ? "Hide" : "Reveal"}</button>
            <button class="btn btn-primary btn-sm" onClick={() => copyField(config()!.api_key, "api-key")}>{copiedField() === "api-key" ? "Copied!" : "Copy"}</button>
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

      <div class="card" style="margin-bottom:16px">
        <div class="card-header">Newznab Indexer</div>
        <div class="card-body">
          <p class="text-secondary" style="margin-bottom:12px">Settings &gt; Indexers &gt; + &gt; Newznab</p>
          <div class="system-row">
            <span class="system-label">Indexer URL</span>
            <span style="display:flex;align-items:center;gap:8px">
              <code style="font-size:12px">{sonarrSetup().indexerUrl}</code>
              <button class="copy-btn" onClick={() => copyField(sonarrSetup().indexerUrl, "indexer-url")}>
                {copiedField() === "indexer-url" ? "Copied!" : "Copy"}
              </button>
            </span>
          </div>
          <div class="system-row">
            <span class="system-label">API Key</span>
            <span style="display:flex;align-items:center;gap:8px">
              <code style="font-size:12px">{config()!.api_key.slice(0, 4)}••••••••{config()!.api_key.slice(-4)}</code>
              <button class="copy-btn" onClick={() => copyField(config()!.api_key, "indexer-key")}>
                {copiedField() === "indexer-key" ? "Copied!" : "Copy"}
              </button>
            </span>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">SABnzbd Download Client</div>
        <div class="card-body">
          <p class="text-secondary" style="margin-bottom:12px">Settings &gt; Download Clients &gt; + &gt; SABnzbd</p>
          {[
            { label: "Host", value: sonarrSetup().sabHost, key: "sab-host" },
            { label: "Port", value: sonarrSetup().sabPort, key: "sab-port" },
            { label: "URL Base", value: sonarrSetup().sabBase, key: "sab-base" },
            { label: "Category", value: sonarrSetup().sabCategory, key: "sab-cat" },
          ].map((row) => (
            <div class="system-row">
              <span class="system-label">{row.label}</span>
              <span style="display:flex;align-items:center;gap:8px">
                <code style="font-size:12px">{row.value}</code>
                <button class="copy-btn" onClick={() => copyField(row.value, row.key)}>
                  {copiedField() === row.key ? "Copied!" : "Copy"}
                </button>
              </span>
            </div>
          ))}
          <div class="system-row">
            <span class="system-label">API Key</span>
            <span style="display:flex;align-items:center;gap:8px">
              <code style="font-size:12px">{config()!.api_key.slice(0, 4)}••••••••{config()!.api_key.slice(-4)}</code>
              <button class="copy-btn" onClick={() => copyField(config()!.api_key, "sab-key")}>
                {copiedField() === "sab-key" ? "Copied!" : "Copy"}
              </button>
            </span>
          </div>
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
