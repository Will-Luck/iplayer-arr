import { createSignal, onMount, Show, For } from "solid-js";
import type { ConfigResponse } from "../types";
import { QUALITY_OPTIONS } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { getSonarrSetup } from "../lib/sonarr-setup";
import { copyToClipboard } from "../lib/clipboard";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Select } from "../ui/Select";
import { Icon } from "../ui/icons";

const QUALITY_SELECT_OPTIONS = QUALITY_OPTIONS.map((q) => ({ value: q, label: q }));
const WORKER_OPTIONS = ["1", "2", "3", "5", "10", "15", "20"];

function maskKey(key: string): string {
  if (key.length <= 8) return key;
  return `${key.slice(0, 4)}••••••••${key.slice(-4)}`;
}

export default function Config() {
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

  function CopyRow(p: { label: string; value: string; field: string; copyValue?: string }) {
    return (
      <div class="flex items-center justify-between gap-3 border-b border-border-subtle py-2 last:border-b-0">
        <span class="text-sm text-text-secondary">{p.label}</span>
        <span class="flex min-w-0 items-center gap-2">
          <code class="truncate rounded bg-elevated px-2 py-1 font-mono text-xs text-text-primary">
            {p.value}
          </code>
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

  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Configuration</h1>
      <Show
        when={config()}
        fallback={
          <Card>
            <Card.Body>
              <p class="text-sm text-text-secondary">Loading...</p>
            </Card.Body>
          </Card>
        }
      >
        <Card>
          <Card.Header>API Key</Card.Header>
          <Card.Body>
            <div class="flex flex-wrap items-center gap-2">
              <code
                class="flex-1 min-w-0 truncate rounded bg-elevated px-3 py-2 font-mono text-sm text-text-primary"
                aria-label="API key"
              >
                {keyRevealed() ? config()!.api_key : maskKey(config()!.api_key)}
              </code>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setKeyRevealed(!keyRevealed())}
                title={keyRevealed() ? "Hide" : "Reveal"}
              >
                {keyRevealed() ? "Hide" : "Reveal"}
              </Button>
              <Button
                size="sm"
                onClick={() => copyField(config()!.api_key, "api-key")}
              >
                <Show
                  when={copiedField() === "api-key"}
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
            </div>
          </Card.Body>
        </Card>

        <Card>
          <Card.Header>Settings</Card.Header>
          <Card.Body>
            <div class="grid gap-x-4 gap-y-4 sm:grid-cols-[200px_1fr] sm:items-start">
              <label
                class="text-sm text-text-secondary sm:pt-2"
                for="cfg-quality"
              >
                Default quality
              </label>
              <div>
                <Select
                  value={config()!.quality}
                  onChange={(v) => updateConfig("quality", v)}
                  options={QUALITY_SELECT_OPTIONS}
                  ariaLabel="Default download quality"
                />
              </div>

              <label
                class="text-sm text-text-secondary sm:pt-2"
                for="cfg-workers"
              >
                Max workers
              </label>
              <div class="flex flex-col gap-1">
                <select
                  id="cfg-workers"
                  class="h-9 w-32 rounded-md border border-border bg-elevated px-3 text-sm text-text-primary transition-colors hover:bg-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
                  value={config()!.max_workers}
                  onChange={(e) => updateConfig("max_workers", e.currentTarget.value)}
                >
                  <Show when={!WORKER_OPTIONS.includes(config()!.max_workers)}>
                    <option value={config()!.max_workers}>{config()!.max_workers}</option>
                  </Show>
                  <For each={WORKER_OPTIONS}>
                    {(workers) => <option value={workers}>{workers}</option>}
                  </For>
                </select>
                <p class="text-xs text-text-tertiary">
                  Number of concurrent download workers. Changes apply after restart.
                </p>
              </div>

              <label
                class="text-sm text-text-secondary sm:pt-2"
                for="cfg-dir"
              >
                Download dir
              </label>
              <input
                id="cfg-dir"
                class="h-9 w-full rounded-md border border-border bg-elevated px-3 text-sm text-text-tertiary"
                type="text"
                value={config()!.download_dir}
                disabled
                aria-disabled="true"
              />

              <label
                class="text-sm text-text-secondary sm:pt-2"
                for="cfg-cleanup"
              >
                Auto cleanup
              </label>
              <div class="flex flex-col gap-1">
                <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-text-primary">
                  <input
                    id="cfg-cleanup"
                    type="checkbox"
                    class="h-4 w-4 rounded border-border bg-elevated accent-accent"
                    checked={config()!.auto_cleanup === "true"}
                    onChange={(e) =>
                      updateConfig(
                        "auto_cleanup",
                        e.currentTarget.checked ? "true" : "false",
                      )
                    }
                  />
                  Remove stale download folders
                </label>
                <p class="text-xs text-text-tertiary">
                  When enabled, folders with no .mp4 files are cleaned up every 5 minutes.
                </p>
              </div>
            </div>
          </Card.Body>
        </Card>

        <Card>
          <Card.Header>Newznab Indexer</Card.Header>
          <Card.Body>
            <p class="mb-2 text-sm text-text-secondary">
              Settings &gt; Indexers &gt; + &gt; Newznab
            </p>
            <CopyRow
              label="Indexer URL"
              value={sonarrSetup().indexerUrl}
              field="indexer-url"
            />
            <CopyRow
              label="API key"
              value={maskKey(config()!.api_key)}
              copyValue={config()!.api_key}
              field="indexer-key"
            />
          </Card.Body>
        </Card>

        <Card>
          <Card.Header>SABnzbd Download Client</Card.Header>
          <Card.Body>
            <p class="mb-2 text-sm text-text-secondary">
              Settings &gt; Download Clients &gt; + &gt; SABnzbd
            </p>
            <CopyRow label="Host" value={sonarrSetup().sabHost} field="sab-host" />
            <CopyRow label="Port" value={sonarrSetup().sabPort} field="sab-port" />
            <CopyRow label="URL base" value={sonarrSetup().sabBase} field="sab-base" />
            <CopyRow
              label="Category"
              value={sonarrSetup().sabCategory}
              field="sab-cat"
            />
            <CopyRow
              label="API key"
              value={maskKey(config()!.api_key)}
              copyValue={config()!.api_key}
              field="sab-key"
            />
          </Card.Body>
        </Card>

        <div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => window.dispatchEvent(new Event("rerun-wizard"))}
          >
            <Icon name="refresh" size={14} />
            Re-run setup wizard
          </Button>
        </div>
      </Show>
    </div>
  );
}
