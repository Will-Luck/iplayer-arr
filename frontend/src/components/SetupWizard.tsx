import { createSignal, onMount, onCleanup, Show } from "solid-js";
import { api, probeApiKey } from "../api";
import { apiKey, setApiKey } from "../apikey";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { copyToClipboard } from "../lib/clipboard";
import { geoBadge } from "../lib/geo";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { Icon } from "../ui/icons";

const maskKey = (k: string) =>
  k.length > 8 ? k.slice(0, 4) + "•".repeat(8) + k.slice(-4) : k;

export default function SetupWizard(props: { show: boolean; onComplete: () => void }) {
  // Step 1 is the API key. It has to come first: every other call the
  // wizard makes is authenticated, so the health check and the Sonarr
  // details cannot load until the operator has supplied a credential.
  const [step, setStep] = createSignal(1);
  const [geoOk, setGeoOk] = createSignal<boolean | null>(null);
  const [geoStatus, setGeoStatus] = createSignal<string | undefined>(undefined);
  const [ffmpegOk, setFfmpegOk] = createSignal<boolean | null>(null);
  const [geoChecking, setGeoChecking] = createSignal(false);
  const [copiedField, setCopiedField] = createSignal<string | null>(null);
  const [keyRevealed, setKeyRevealed] = createSignal(false);
  const [keyDraft, setKeyDraft] = createSignal("");
  const [keyChecking, setKeyChecking] = createSignal(false);
  const [keyError, setKeyError] = createSignal("");
  const [editingKey, setEditingKey] = createSignal(false);
  const sonarrSetup = () => getSonarrSetup(window.location);

  const displayKey = (k: string) => (keyRevealed() ? k : maskKey(k));
  const showKeyForm = () => editingKey() || apiKey() === "";

  async function loadHealth() {
    try {
      const status = await api.getStatus();
      setFfmpegOk(!!status.ffmpeg);
      setGeoStatus(status.geo_status);
      setGeoOk(geoBadge(status.geo_status, status.geo_ok).ok);
    } catch {
      // Unauthenticated or server not up yet: leave the pills unknown
      // rather than claiming a failure we have not measured.
      setFfmpegOk(null);
      setGeoOk(null);
    }
  }

  onMount(() => {
    if (apiKey()) loadHealth();

    const onKey = (e: KeyboardEvent) => {
      if (props.show && e.key === "Escape") {
        e.preventDefault();
        props.onComplete();
      }
    };
    window.addEventListener("keydown", onKey);
    onCleanup(() => window.removeEventListener("keydown", onKey));
  });

  async function saveKey() {
    const candidate = keyDraft().trim();
    if (candidate === "") {
      setKeyError("Enter the API key.");
      return;
    }
    setKeyChecking(true);
    setKeyError("");
    try {
      if (!(await probeApiKey(candidate))) {
        setKeyError("That key was rejected by the server. Check the value and try again.");
        return;
      }
      setApiKey(candidate);
      setKeyDraft("");
      setEditingKey(false);
      await loadHealth();
      setStep(2);
    } finally {
      setKeyChecking(false);
    }
  }

  async function goToHealth() {
    await loadHealth();
    setStep(2);
  }

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
            <div class={stepClass(3)} />
          </div>

          <Show when={step() === 1}>
            <h2 class="mb-2 text-lg font-semibold text-text-primary">API key</h2>
            <p class="mb-4 text-sm text-text-secondary">
              The dashboard, the Newznab indexer and the SABnzbd download client all
              share one key. Enter it here to unlock this browser.
            </p>

            <Show when={showKeyForm()}>
              <Card class="mb-4">
                <Card.Header>Where to find it</Card.Header>
                <Card.Body>
                  <p class="mb-2 text-sm text-text-secondary">
                    iplayer-arr writes the key to a file in its config directory on
                    every start:
                  </p>
                  <code class="mb-3 block rounded bg-elevated px-3 py-2 font-mono text-xs text-text-primary">
                    docker exec &lt;container&gt; cat /config/api_key
                  </code>
                  <p class="text-sm text-text-secondary">
                    You can also pin your own value with the{" "}
                    <code class="rounded bg-elevated px-1 py-0.5 font-mono text-xs">API_KEY</code>{" "}
                    environment variable and restart the container.
                  </p>
                </Card.Body>
              </Card>

              <label class="mb-1 block text-sm text-text-secondary" for="wizard-api-key">
                API key
              </label>
              <input
                id="wizard-api-key"
                class="mb-2 h-9 w-full rounded-md border border-border bg-elevated px-3 font-mono text-sm text-text-primary"
                type="text"
                autocomplete="off"
                spellcheck={false}
                value={keyDraft()}
                onInput={(e) => setKeyDraft(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") saveKey();
                }}
              />
              <Show when={keyError()}>
                <p class="mb-2 text-xs text-danger">{keyError()}</p>
              </Show>

              <div class="flex items-center gap-2">
                <Show when={editingKey() && apiKey()}>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => {
                      setEditingKey(false);
                      setKeyDraft("");
                      setKeyError("");
                    }}
                  >
                    Cancel
                  </Button>
                </Show>
                <Button size="sm" class="ml-auto" loading={keyChecking()} onClick={saveKey}>
                  {keyChecking() ? "Checking..." : "Save and continue"}
                </Button>
              </div>
            </Show>

            <Show when={!showKeyForm()}>
              <Card class="mb-4">
                <Card.Body>
                  <CopyRow
                    label="API key"
                    value={displayKey(apiKey())}
                    copyValue={apiKey()}
                    field="wizard-key"
                    trailing={<RevealButton />}
                  />
                </Card.Body>
              </Card>
              <div class="flex items-center gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    setEditingKey(true);
                    setKeyDraft("");
                    setKeyError("");
                  }}
                >
                  Use a different key
                </Button>
                <Button size="sm" class="ml-auto" onClick={goToHealth}>
                  Next
                </Button>
              </div>
            </Show>
          </Show>

          <Show when={step() === 2}>
            <h2 class="mb-2 text-lg font-semibold text-text-primary">Health checks</h2>
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
              <Button variant="secondary" size="sm" onClick={() => setStep(1)}>
                Back
              </Button>
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
                onClick={() => setStep(3)}
              >
                Next
              </Button>
            </div>
          </Show>

          <Show when={step() === 3}>
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
                <CopyRow
                  label="API key"
                  value={displayKey(apiKey())}
                  copyValue={apiKey()}
                  field="indexer-key"
                  trailing={<RevealButton />}
                />
              </Card.Body>
            </Card>

            <Card class="mb-4">
              <Card.Header>SABnzbd download client</Card.Header>
              <Card.Body>
                <CopyRow label="Host" value={sonarrSetup().sabHost} field="sab-host" />
                <CopyRow label="Port" value={sonarrSetup().sabPort} field="sab-port" />
                <CopyRow label="URL base" value={sonarrSetup().sabBase} field="sab-base" />
                <CopyRow label="Category" value={sonarrSetup().sabCategory} field="sab-cat" />
                <CopyRow
                  label="API key"
                  value={displayKey(apiKey())}
                  copyValue={apiKey()}
                  field="sab-key"
                  trailing={<RevealButton />}
                />
              </Card.Body>
            </Card>

            <div class="flex items-center gap-2">
              <Button variant="secondary" size="sm" onClick={() => setStep(2)}>
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
