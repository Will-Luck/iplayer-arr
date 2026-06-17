import { createSignal, onMount, onCleanup, Show } from "solid-js";
import { api } from "../api";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { copyToClipboard } from "../lib/clipboard";
import { geoBadge } from "../lib/geo";
import type { ConfigResponse } from "../types";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { Icon } from "../ui/icons";

const maskKey = (k: string) =>
  k.length > 8 ? k.slice(0, 4) + "•".repeat(8) + k.slice(-4) : k;

export default function SetupWizard(props: { show: boolean; onComplete: () => void }) {
  const [step, setStep] = createSignal(1);
  const [geoOk, setGeoOk] = createSignal<boolean | null>(null);
  const [geoStatus, setGeoStatus] = createSignal<string | undefined>(undefined);
  const [ffmpegOk, setFfmpegOk] = createSignal<boolean | null>(null);
  const [geoChecking, setGeoChecking] = createSignal(false);
  const [config, setConfig] = createSignal<ConfigResponse | null>(null);
  const [copiedField, setCopiedField] = createSignal<string | null>(null);
  const [keyRevealed, setKeyRevealed] = createSignal(false);
  const sonarrSetup = () => getSonarrSetup(window.location);

  const displayKey = (k: string) => (keyRevealed() ? k : maskKey(k));

  onMount(async () => {
    try {
      const status = await api.getStatus();
      setFfmpegOk(!!status.ffmpeg);
      setGeoStatus(status.geo_status);
      setGeoOk(geoBadge(status.geo_status, status.geo_ok).ok);
    } catch {
      // ignore
    }
    try {
      setConfig(await api.getConfig());
    } catch {
      // ignore
    }

    const onKey = (e: KeyboardEvent) => {
      if (props.show && e.key === "Escape") {
        e.preventDefault();
        props.onComplete();
      }
    };
    window.addEventListener("keydown", onKey);
    onCleanup(() => window.removeEventListener("keydown", onKey));
  });

  async function runGeoCheck() {
    setGeoChecking(true);
    try {
      const result = await api.geoCheck();
      setGeoStatus(result.geo_status);
      setGeoOk(geoBadge(result.geo_status, result.geo_ok).ok);
    } catch {
      setGeoStatus(undefined);
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

  function StatusPill(p: { ok: boolean | null }) {
    return (
      <Show
        when={p.ok !== null}
        fallback={<Badge variant="neutral">Unknown</Badge>}
      >
        <Badge variant={p.ok ? "completed" : "failed"}>{p.ok ? "OK" : "Failed"}</Badge>
      </Show>
    );
  }

  function CopyRow(p: { label: string; value: string; field: string; copyValue?: string; trailing?: import("solid-js").JSX.Element }) {
    return (
      <div class="flex items-center justify-between gap-3 border-b border-border-subtle py-2 last:border-b-0">
        <span class="text-sm text-text-secondary">{p.label}</span>
        <span class="flex flex-wrap items-center gap-2">
          <code class="rounded bg-elevated px-2 py-1 font-mono text-xs text-text-primary">
            {p.value}
          </code>
          {p.trailing}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => copyField(p.copyValue ?? p.value, p.field)}
            aria-label={`Copy ${p.label}`}
          >
            <Show
              when={copiedField() === p.field}
              fallback={
                <>
                  <Icon name="copy" size={14} />
                  Copy
                </>
              }
            >
              <Icon name="check" size={14} />
              Copied
            </Show>
          </Button>
        </span>
      </div>
    );
  }

  const RevealButton = () => (
    <Button
      variant="secondary"
      size="sm"
      onClick={() => setKeyRevealed(!keyRevealed())}
      aria-pressed={keyRevealed()}
    >
      {keyRevealed() ? "Hide" : "Reveal"}
    </Button>
  );

  return (
    <Show when={props.show}>
      <div
        class="wizard-overlay"
        role="dialog"
        aria-modal="true"
        aria-label="Setup wizard"
        onClick={() => props.onComplete()}
      >
        <div class="wizard-modal" onClick={(e) => e.stopPropagation()}>
          <button
            type="button"
            class="wizard-close"
            aria-label="Close setup wizard"
            onClick={() => props.onComplete()}
          >
            ×
          </button>
          <div class="wizard-progress" aria-label="Setup progress">
            <div class={stepClass(1)} />
            <div class={stepClass(2)} />
          </div>

          <Show when={step() === 1}>
            <h2 class="mb-2 text-lg font-semibold text-text-primary">Welcome to iplayer-arr</h2>
            <p class="mb-5 text-sm text-text-secondary">
              Let's make sure everything is ready before you start.
            </p>

            <Card class="mb-4">
              <Card.Body>
                <div class="flex items-center justify-between border-b border-border-subtle py-2">
                  <span class="text-sm text-text-primary">UK geo access</span>
                  <StatusPill ok={geoOk()} />
                </div>
                <div class="flex items-center justify-between py-2">
                  <span class="text-sm text-text-primary">ffmpeg</span>
                  <StatusPill ok={ffmpegOk()} />
                </div>
              </Card.Body>
            </Card>

            <Show when={geoOk() === false}>
              <p class="mb-3 text-xs text-text-secondary">
                {geoBadge(geoStatus(), false).detail ||
                  "iplayer-arr must reach BBC iPlayer. Ensure your container routes through a UK VPN."}
              </p>
            </Show>

            <Show when={ffmpegOk() === false}>
              <p class="mb-3 text-xs text-text-secondary">
                ffmpeg was not found. Install it in your container or set the FFMPEG_PATH environment variable.
              </p>
            </Show>

            <div class="flex items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={runGeoCheck}
                loading={geoChecking()}
              >
                {geoChecking() ? "Checking..." : "Re-check geo"}
              </Button>
              <Button
                size="sm"
                class="ml-auto"
                disabled={!geoOk()}
                onClick={() => setStep(2)}
              >
                Next
              </Button>
            </div>
          </Show>

          <Show when={step() === 2}>
            <h2 class="mb-2 text-lg font-semibold text-text-primary">Sonarr setup</h2>
            <p class="mb-5 text-sm text-text-secondary">
              Add iplayer-arr to Sonarr using the values below.
            </p>

            <Card class="mb-4">
              <Card.Header>Newznab indexer</Card.Header>
              <Card.Body>
                <CopyRow
                  label="Indexer URL"
                  value={sonarrSetup().indexerUrl}
                  field="indexer-url"
                />
                <Show
                  when={config()?.api_key}
                  fallback={
                    <div class="flex items-center justify-between gap-3 py-2">
                      <span class="text-sm text-text-secondary">API key</span>
                      <span class="text-text-tertiary">-</span>
                    </div>
                  }
                >
                  <CopyRow
                    label="API key"
                    value={displayKey(config()!.api_key)}
                    copyValue={config()!.api_key}
                    field="indexer-key"
                    trailing={<RevealButton />}
                  />
                </Show>
              </Card.Body>
            </Card>

            <Card class="mb-4">
              <Card.Header>SABnzbd download client</Card.Header>
              <Card.Body>
                <CopyRow label="Host" value={sonarrSetup().sabHost} field="sab-host" />
                <CopyRow label="Port" value={sonarrSetup().sabPort} field="sab-port" />
                <CopyRow label="URL base" value={sonarrSetup().sabBase} field="sab-base" />
                <CopyRow label="Category" value={sonarrSetup().sabCategory} field="sab-cat" />
                <Show
                  when={config()?.api_key}
                  fallback={
                    <div class="flex items-center justify-between gap-3 py-2">
                      <span class="text-sm text-text-secondary">API key</span>
                      <span class="text-text-tertiary">-</span>
                    </div>
                  }
                >
                  <CopyRow
                    label="API key"
                    value={displayKey(config()!.api_key)}
                    copyValue={config()!.api_key}
                    field="sab-key"
                    trailing={<RevealButton />}
                  />
                </Show>
              </Card.Body>
            </Card>

            <div class="flex items-center gap-2">
              <Button variant="secondary" size="sm" onClick={() => setStep(1)}>
                Back
              </Button>
              <Button size="sm" class="ml-auto" onClick={props.onComplete}>
                Done
              </Button>
            </div>
          </Show>
        </div>
      </div>
    </Show>
  );
}
