import { createSignal, onMount, Show } from "solid-js";
import { api } from "../api";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { copyToClipboard } from "../lib/clipboard";
import type { ConfigResponse } from "../types";

export default function SetupWizard(props: { show: boolean; onComplete: () => void }) {
  const [step, setStep] = createSignal(1);
  const [geoOk, setGeoOk] = createSignal<boolean | null>(null);
  const [ffmpegOk, setFfmpegOk] = createSignal<boolean | null>(null);
  const [geoChecking, setGeoChecking] = createSignal(false);
  const [config, setConfig] = createSignal<ConfigResponse | null>(null);
  const [copiedField, setCopiedField] = createSignal<string | null>(null);
  const sonarrSetup = () => getSonarrSetup(window.location);

  onMount(async () => {
    try {
      const status = await api.getStatus();
      setFfmpegOk(!!status.ffmpeg);
      setGeoOk(status.geo_ok);
    } catch {
      // ignore
    }
    try {
      setConfig(await api.getConfig());
    } catch {
      // ignore
    }
  });

  async function runGeoCheck() {
    setGeoChecking(true);
    try {
      const result = await api.geoCheck();
      setGeoOk(result.geo_ok);
    } catch {
      setGeoOk(false);
    } finally {
      setGeoChecking(false);
    }
  }

  async function copyField(text: string, key: string) {
    const ok = await copyToClipboard(text);
    if (!ok) return;
    setCopiedField(key);
    setTimeout(() => setCopiedField(null), 2000);
  }

  function stepClass(n: number) {
    const s = step();
    if (n < s) return "wizard-step done";
    if (n === s) return "wizard-step active";
    return "wizard-step";
  }

  function StatusDot(p: { ok: boolean | null; label: string }) {
    return (
      <span>
        <Show when={p.ok !== null} fallback={<span class="text-secondary">—</span>}>
          <span
            class="status-dot"
            classList={{ ok: p.ok === true, err: p.ok === false }}
            aria-label={p.label}
          />
          {p.ok ? "OK" : "Failed"}
        </Show>
      </span>
    );
  }

  return (
    <Show when={props.show}>
      <div class="wizard-overlay" role="dialog" aria-modal="true" aria-label="Setup wizard">
        <div class="wizard-modal">
          <div class="wizard-progress" aria-label="Setup progress">
            <div class={stepClass(1)} />
            <div class={stepClass(2)} />
          </div>

          <Show when={step() === 1}>
            <h2 class="page-title" style="margin-bottom:8px">Welcome to iplayer-arr</h2>
            <p class="text-secondary" style="margin-bottom:20px">
              Let's make sure everything is ready before you start.
            </p>

            <div class="card" style="margin-bottom:16px">
              <div class="card-body">
                <div class="system-row">
                  <span>UK geo access</span>
                  <StatusDot ok={geoOk()} label={geoOk() ? "Geo OK" : "Geo failed"} />
                </div>
                <div class="system-row">
                  <span>ffmpeg</span>
                  <StatusDot ok={ffmpegOk()} label={ffmpegOk() ? "ffmpeg found" : "ffmpeg missing"} />
                </div>
              </div>
            </div>

            <Show when={geoOk() === false}>
              <p class="text-secondary" style="margin-bottom:12px;font-size:13px">
                iplayer-arr must reach BBC iPlayer. Ensure your container routes through a UK VPN.
              </p>
            </Show>

            <Show when={ffmpegOk() === false}>
              <p class="text-secondary" style="margin-bottom:12px;font-size:13px">
                ffmpeg was not found. Install it in your container or set the FFMPEG_PATH environment variable.
              </p>
            </Show>

            <div style="display:flex;gap:8px;align-items:center;margin-top:4px">
              <button
                class="btn btn-sm"
                onClick={runGeoCheck}
                disabled={geoChecking()}
              >
                {geoChecking() ? "Checking..." : "Re-check geo"}
              </button>
              <button
                class="btn btn-primary btn-sm"
                style="margin-left:auto"
                disabled={!geoOk()}
                onClick={() => setStep(2)}
              >
                Next
              </button>
            </div>
          </Show>

          <Show when={step() === 2}>
            <h2 class="page-title" style="margin-bottom:8px">Sonarr Setup</h2>
            <p class="text-secondary" style="margin-bottom:20px">
              Add iplayer-arr to Sonarr using the values below.
            </p>

            <div class="card" style="margin-bottom:16px">
              <div class="card-header">Newznab Indexer</div>
              <div class="card-body">
                <div class="system-row">
                  <span class="system-label">Indexer URL</span>
                  <span style="display:flex;align-items:center;gap:8px">
                    <code style="font-size:12px">{sonarrSetup().indexerUrl}</code>
                    <button
                      class="copy-btn"
                      onClick={() => copyField(sonarrSetup().indexerUrl, "indexer-url")}
                    >
                      {copiedField() === "indexer-url" ? "Copied!" : "Copy"}
                    </button>
                  </span>
                </div>
                <div class="system-row">
                  <span class="system-label">API Key</span>
                  <span style="display:flex;align-items:center;gap:8px">
                    <code style="font-size:12px">{config()?.api_key ?? "—"}</code>
                    <Show when={config()?.api_key}>
                      <button
                        class="copy-btn"
                        onClick={() => copyField(config()!.api_key, "indexer-key")}
                      >
                        {copiedField() === "indexer-key" ? "Copied!" : "Copy"}
                      </button>
                    </Show>
                  </span>
                </div>
              </div>
            </div>

            <div class="card" style="margin-bottom:16px">
              <div class="card-header">SABnzbd Download Client</div>
              <div class="card-body">
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
                      <button
                        class="copy-btn"
                        onClick={() => copyField(row.value, row.key)}
                      >
                        {copiedField() === row.key ? "Copied!" : "Copy"}
                      </button>
                    </span>
                  </div>
                ))}
                <div class="system-row">
                  <span class="system-label">API Key</span>
                  <span style="display:flex;align-items:center;gap:8px">
                    <code style="font-size:12px">{config()?.api_key ?? "—"}</code>
                    <Show when={config()?.api_key}>
                      <button
                        class="copy-btn"
                        onClick={() => copyField(config()!.api_key, "sab-key")}
                      >
                        {copiedField() === "sab-key" ? "Copied!" : "Copy"}
                      </button>
                    </Show>
                  </span>
                </div>
              </div>
            </div>

            <div style="display:flex;gap:8px;align-items:center">
              <button class="btn btn-sm" onClick={() => setStep(1)}>
                Back
              </button>
              <button
                class="btn btn-primary btn-sm"
                style="margin-left:auto"
                onClick={props.onComplete}
              >
                Done
              </button>
            </div>
          </Show>
        </div>
      </div>
    </Show>
  );
}
