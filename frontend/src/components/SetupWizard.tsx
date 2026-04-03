import { createSignal, onMount, Show } from "solid-js";
import { api } from "../api";
import type { ConfigResponse } from "../types";

export default function SetupWizard(props: { show: boolean; onComplete: () => void }) {
  const [step, setStep] = createSignal(1);
  const [geoOk, setGeoOk] = createSignal<boolean | null>(null);
  const [ffmpegOk, setFfmpegOk] = createSignal<boolean | null>(null);
  const [geoChecking, setGeoChecking] = createSignal(false);
  const [config, setConfig] = createSignal<ConfigResponse | null>(null);
  const [testingIndexer, setTestingIndexer] = createSignal(false);
  const [indexerOk, setIndexerOk] = createSignal<boolean | null>(null);
  const [testingSab, setTestingSab] = createSignal(false);
  const [sabOk, setSabOk] = createSignal<boolean | null>(null);
  const [copiedField, setCopiedField] = createSignal<string | null>(null);

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

  function copyField(text: string, key: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopiedField(key);
      setTimeout(() => setCopiedField(null), 2000);
    });
  }

  async function testIndexer() {
    setTestingIndexer(true);
    setIndexerOk(null);
    try {
      const res = await fetch("/newznab/api?t=caps");
      setIndexerOk(res.ok);
    } catch {
      setIndexerOk(false);
    } finally {
      setTestingIndexer(false);
    }
  }

  async function testSab() {
    setTestingSab(true);
    setSabOk(null);
    try {
      const res = await fetch("/sabnzbd/api?mode=version");
      setSabOk(res.ok);
    } catch {
      setSabOk(false);
    } finally {
      setTestingSab(false);
    }
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
          {/* Progress bar */}
          <div class="wizard-progress" aria-label="Setup progress">
            <div class={stepClass(1)} />
            <div class={stepClass(2)} />
            <div class={stepClass(3)} />
          </div>

          {/* Step 1: Welcome & Health Check */}
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

          {/* Step 2: Sonarr Indexer Setup */}
          <Show when={step() === 2}>
            <h2 class="page-title" style="margin-bottom:8px">Sonarr Indexer Setup</h2>
            <p class="text-secondary" style="margin-bottom:20px">
              Add iplayer-arr as a Newznab indexer in Sonarr (Settings &gt; Indexers).
            </p>

            <div class="card" style="margin-bottom:16px">
              <div class="card-body">
                <div class="system-row">
                  <span class="system-label">Indexer URL</span>
                  <span style="display:flex;align-items:center;gap:8px">
                    <code style="font-size:12px">http://&lt;host&gt;:&lt;port&gt;/newznab/api</code>
                  </span>
                </div>
                <div class="system-row">
                  <span class="system-label">API Key</span>
                  <span style="display:flex;align-items:center;gap:8px">
                    <code style="font-size:12px">{config()?.api_key ?? "—"}</code>
                    <Show when={config()}>
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

            <div style="display:flex;gap:8px;align-items:center">
              <button class="btn btn-sm" onClick={testIndexer} disabled={testingIndexer()}>
                {testingIndexer() ? "Testing..." : "Test Connection"}
              </button>
              <Show when={indexerOk() !== null}>
                <span class={indexerOk() ? "text-success" : "text-danger"} style="font-size:13px">
                  {indexerOk() ? "Connected" : "Failed"}
                </span>
              </Show>
              <button
                class="btn btn-primary btn-sm"
                style="margin-left:auto"
                onClick={() => setStep(3)}
              >
                Next
              </button>
            </div>
          </Show>

          {/* Step 3: Sonarr Download Client Setup */}
          <Show when={step() === 3}>
            <h2 class="page-title" style="margin-bottom:8px">Sonarr Download Client</h2>
            <p class="text-secondary" style="margin-bottom:20px">
              Add iplayer-arr as a SABnzbd download client in Sonarr (Settings &gt; Download Clients).
            </p>

            <div class="card" style="margin-bottom:16px">
              <div class="card-body">
                {[
                  { label: "Host", value: "<host>", key: "sab-host" },
                  { label: "Port", value: "<port>", key: "sab-port" },
                  { label: "URL Base", value: "/sabnzbd", key: "sab-base" },
                  { label: "Category", value: "sonarr", key: "sab-cat" },
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
                    <Show when={config()}>
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
              <button class="btn btn-sm" onClick={testSab} disabled={testingSab()}>
                {testingSab() ? "Testing..." : "Test"}
              </button>
              <Show when={sabOk() !== null}>
                <span class={sabOk() ? "text-success" : "text-danger"} style="font-size:13px">
                  {sabOk() ? "Connected" : "Failed"}
                </span>
              </Show>
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
