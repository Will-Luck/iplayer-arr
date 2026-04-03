import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import type { DirectoryEntry } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
}

export default function Downloads() {
  const [entries, setEntries] = createSignal<DirectoryEntry[]>([]);
  const [loading, setLoading] = createSignal(true);

  async function loadDirectory() {
    try {
      setEntries(await api.listDirectory());
    } catch {
      // API may not be available
    } finally {
      setLoading(false);
    }
  }

  async function deleteFolder(name: string) {
    if (!confirm(`Delete folder "${name}" and all its contents?`)) return;
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
    <div>
      <h1 class="page-title">Downloads Directory</h1>

      <Show when={!loading()} fallback={<p class="text-muted">Loading...</p>}>
        <div class="card">
          <div class="card-header">
            Folders ({entries().length})
            <button class="btn btn-primary btn-sm" style="margin-left:auto" onClick={loadDirectory}>Refresh</button>
          </div>
          <Show when={entries().length > 0} fallback={<div class="card-empty">Downloads directory is empty</div>}>
            <table class="table">
              <thead>
                <tr>
                  <th scope="col">Folder</th>
                  <th scope="col">Files</th>
                  <th scope="col">Size</th>
                  <th scope="col">Owner</th>
                  <th scope="col">Actions</th>
                </tr>
              </thead>
              <tbody>
                <For each={entries()}>
                  {(entry) => (
                    <tr>
                      <td title={entry.name} style="max-width:400px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
                        {entry.name}
                      </td>
                      <td class="text-muted">
                        {entry.files.length} {entry.files.length === 1 ? "file" : "files"}
                        <Show when={entry.files.length > 0}>
                          <br/>
                          <span style="font-size:11px;color:var(--muted)">{entry.files.map(f => f.name.split('.').pop()).filter((v, i, a) => a.indexOf(v) === i).join(", ")}</span>
                        </Show>
                      </td>
                      <td class="text-muted">{formatBytes(entry.total_size)}</td>
                      <td>
                        <span class={`badge badge-${entry.owned ? "completed" : "pending"}`}>
                          {entry.owned ? "iplayer-arr" : "unknown"}
                        </span>
                      </td>
                      <td>
                        <button
                          class="btn btn-sm"
                          style="background:var(--danger);color:white"
                          disabled={!entry.owned}
                          title={entry.owned ? "Delete folder" : "Cannot delete: not owned by iplayer-arr"}
                          onClick={() => deleteFolder(entry.name)}
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </Show>
        </div>
      </Show>
    </div>
  );
}
