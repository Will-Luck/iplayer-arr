import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import type { DirectoryEntry } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { confirmDialog } from "../confirm";
import { Card } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { IconButton } from "../ui/IconButton";
import { EmptyState } from "../ui/EmptyState";
import { Table } from "../ui/Table";
import { Icon } from "../ui/icons";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
}

function uniqueExtensions(files: { name: string }[]): string {
  const seen = new Set<string>();
  for (const f of files) {
    const ext = f.name.split(".").pop();
    if (ext) seen.add(ext);
  }
  return Array.from(seen).join(", ");
}

export default function Downloads() {
  const [entries, setEntries] = createSignal<DirectoryEntry[]>([]);
  const [loading, setLoading] = createSignal(true);
  const [error, setError] = createSignal<string | null>(null);

  async function loadDirectory() {
    try {
      setEntries(await api.listDirectory());
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load directory");
    } finally {
      setLoading(false);
    }
  }

  async function deleteFolder(name: string) {
    const ok = await confirmDialog({
      title: "Delete folder?",
      message: `Delete folder "${name}" and all its contents?`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteDirectoryFolder(name);
      addToast("success", `Deleted ${name}`);
      loadDirectory();
    } catch (e) {
      addToast("error", `Failed to delete: ${e instanceof Error ? e.message : "unknown"}`);
    }
  }

  onMount(() => {
    loadDirectory();
    const interval = setInterval(loadDirectory, 30000);
    onCleanup(() => clearInterval(interval));
  });

  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Downloads Directory</h1>

      <Show when={error()}>
        <Card>
          <Card.Body>
            <p class="text-sm text-danger">Failed to load downloads: {error()}</p>
          </Card.Body>
        </Card>
      </Show>

      <Show when={!loading()} fallback={
        <Card>
          <Card.Body>
            <p class="text-sm text-text-secondary">Loading...</p>
          </Card.Body>
        </Card>
      }>
        <Card>
          <Card.Header
            actions={
              <Button variant="secondary" size="sm" onClick={loadDirectory}>
                <Icon name="refresh" size={14} />
                Refresh
              </Button>
            }
          >
            Folders ({entries().length})
          </Card.Header>
          <Show
            when={entries().length > 0}
            fallback={
              <Card.Body padded={false}>
                <EmptyState
                  icon="archive"
                  title="Downloads directory is empty"
                  description="Queued downloads will appear here once started."
                />
              </Card.Body>
            }
          >
            <Card.Body padded={false}>
              <Table collapse="card">
                <Table.THead>
                  <Table.TR>
                    <Table.TH name="folder">Folder</Table.TH>
                    <Table.TH name="files" width={160}>Files</Table.TH>
                    <Table.TH name="size" align="right" width={100}>Size</Table.TH>
                    <Table.TH name="owner" width={130}>Owner</Table.TH>
                    <Table.TH name="actions" align="right" width={80}>
                      <span class="sr-only">Actions</span>
                    </Table.TH>
                  </Table.TR>
                </Table.THead>
                <Table.TBody>
                  <For each={entries()}>
                    {(entry) => (
                      <Table.TR>
                        <Table.TD primary label="Folder">
                          <span title={entry.name} class="block truncate">{entry.name}</span>
                        </Table.TD>
                        <Table.TD muted label="Files">
                          {entry.files.length} {entry.files.length === 1 ? "file" : "files"}
                          <Show when={entry.files.length > 0}>
                            <div class="text-xs text-text-tertiary">
                              {uniqueExtensions(entry.files)}
                            </div>
                          </Show>
                        </Table.TD>
                        <Table.TD muted tabular align="right" label="Size">
                          {formatBytes(entry.total_size)}
                        </Table.TD>
                        <Table.TD label="Owner">
                          <Badge variant={entry.owned ? "completed" : "pending"}>
                            {entry.owned ? "iplayer-arr" : "unknown"}
                          </Badge>
                        </Table.TD>
                        <Table.TD align="right" label="Actions">
                          <IconButton
                            icon="trash"
                            tone="danger"
                            size="sm"
                            disabled={!entry.owned}
                            aria-label={`Delete ${entry.name}`}
                            title={entry.owned ? "Delete folder" : "Cannot delete: not owned by iplayer-arr"}
                            onClick={() => deleteFolder(entry.name)}
                          />
                        </Table.TD>
                      </Table.TR>
                    )}
                  </For>
                </Table.TBody>
              </Table>
            </Card.Body>
          </Show>
        </Card>
      </Show>
    </div>
  );
}
