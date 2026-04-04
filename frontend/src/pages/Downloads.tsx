import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import type { DirectoryEntry } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
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

      <Show when={error()}>
        <p class="text-error">Failed to load downloads: {error()}</p>
      </Show>

      <Show when={!loading()} fallback={<p class="text-muted">Loading...</p>}>
        <div class="card">
          <div class="card-header" style={{ display: "flex", "align-items": "center", gap: "8px" }}>
            <span>Folders ({entries().length})</span>
            <button class="btn btn-primary btn-sm ml-auto" onClick={loadDirectory}>Refresh</button>
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
                      <td class="dl-folder-name" title={entry.name}>
                        {entry.name}
                      </td>
                      <td class="text-muted">
                        {entry.files.length} {entry.files.length === 1 ? "file" : "files"}
                        <Show when={entry.files.length > 0}>
                          <br/>
                          <span class="dl-file-types">{entry.files.map(f => f.name.split('.').pop()).filter((v, i, a) => a.indexOf(v) === i).join(", ")}</span>
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
                          class="btn btn-sm btn-delete"
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
