import { createSignal, onMount, For, Show } from "solid-js";
import type { ShowOverride } from "../types";
import { api } from "../api";
import { addToast } from "../toast";

const emptyOverride = (): ShowOverride => ({
  show_name: "", force_date_based: false, force_series_num: 0,
  force_position: false, series_offset: 0, episode_offset: 0, custom_name: "",
});

export default function Overrides() {
  const [overrides, setOverrides] = createSignal<ShowOverride[]>([]);
  const [editing, setEditing] = createSignal<string | null>(null);
  const [adding, setAdding] = createSignal(false);
  const [draft, setDraft] = createSignal<ShowOverride>(emptyOverride());
  const [nameError, setNameError] = createSignal("");

  onMount(async () => { setOverrides(await api.listOverrides()); });

  async function save() {
    const o = draft();
    if (adding() && !o.show_name.trim()) {
      setNameError("Show name is required");
      return;
    }
    setNameError("");
    try {
      await api.putOverride(o);
      setOverrides(await api.listOverrides());
      setEditing(null);
      setAdding(false);
      addToast("success", "Override saved");
    } catch (e) {
      addToast("error", `Failed to save override: ${e instanceof Error ? e.message : "unknown error"}`);
    }
  }

  async function remove(show: string) {
    if (!confirm(`Delete override for "${show}"?`)) return;
    try {
      await api.deleteOverride(show);
      setOverrides(await api.listOverrides());
      addToast("success", "Override deleted");
    } catch (e) {
      addToast("error", `Failed to delete override: ${e instanceof Error ? e.message : "unknown error"}`);
    }
  }

  function startEdit(o: ShowOverride) {
    setDraft({ ...o });
    setEditing(o.show_name);
  }

  function startAdd() {
    setDraft(emptyOverride());
    setAdding(true);
  }

  function updateDraft(field: keyof ShowOverride, value: string | number | boolean) {
    setDraft(prev => ({ ...prev, [field]: value }));
  }

  const editRow = () => (
    <tr>
      <td>
        <input class="input override-input-name" value={draft().show_name} onInput={e => { updateDraft("show_name", e.target.value); setNameError(""); }} disabled={!!editing()} aria-label="Show name" />
        <Show when={nameError()}>
          <p class="text-danger field-error">{nameError()}</p>
        </Show>
      </td>
      <td class="text-center"><input type="checkbox" checked={draft().force_date_based} onChange={e => updateDraft("force_date_based", e.target.checked)} aria-label="Force date-based" /></td>
      <td><input class="input override-input-num" type="number" value={draft().force_series_num} onInput={e => updateDraft("force_series_num", +e.target.value)} aria-label="Force series number" /></td>
      <td><input class="input override-input-num" type="number" value={draft().series_offset} onInput={e => updateDraft("series_offset", +e.target.value)} aria-label="Series offset" /></td>
      <td><input class="input override-input-num" type="number" value={draft().episode_offset} onInput={e => updateDraft("episode_offset", +e.target.value)} aria-label="Episode offset" /></td>
      <td><input class="input override-input-custom" value={draft().custom_name} onInput={e => updateDraft("custom_name", e.target.value)} aria-label="Custom name" /></td>
      <td>
        <div class="override-actions">
          <button class="btn btn-primary btn-sm" onClick={save}>Save</button>
          <button class="btn btn-cancel btn-sm" onClick={() => { setEditing(null); setAdding(false); }}>Cancel</button>
        </div>
      </td>
    </tr>
  );

  return (
    <div class="card">
      <div class="card-header override-header">
        <span>Show Overrides</span>
        <button class="btn btn-primary btn-sm" onClick={startAdd} disabled={adding()}>Add Override</button>
      </div>
      <div class="card-body overrides-body">
        <p class="text-muted overrides-desc">
          Override how specific shows are numbered. Force date-based for daily shows, adjust series/episode offsets for mismatched numbering.
        </p>
        <table class="table">
          <caption class="sr-only">Show name overrides</caption>
          <thead>
            <tr>
              <th scope="col">Show Name</th>
              <th scope="col">Date-Based</th>
              <th scope="col">Force Series</th>
              <th scope="col">Series Offset</th>
              <th scope="col">Ep Offset</th>
              <th scope="col">Custom Name</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            <Show when={adding()}>{editRow()}</Show>
            <For each={overrides()}>{o => (
              <Show when={editing() === o.show_name} fallback={
                <tr>
                  <td>{o.show_name}</td>
                  <td>{o.force_date_based ? "Yes" : "No"}</td>
                  <td>{o.force_series_num || "-"}</td>
                  <td>{o.series_offset || "-"}</td>
                  <td>{o.episode_offset || "-"}</td>
                  <td>{o.custom_name || "-"}</td>
                  <td>
                    <div class="override-actions">
                      <button class="btn btn-primary btn-sm" onClick={() => startEdit(o)} aria-label={`Edit ${o.show_name}`}>Edit</button>
                      <button class="btn btn-danger btn-sm" onClick={() => remove(o.show_name)} aria-label={`Delete ${o.show_name}`}>Delete</button>
                    </div>
                  </td>
                </tr>
              }>
                {editRow()}
              </Show>
            )}</For>
          </tbody>
        </table>
        <Show when={overrides().length === 0 && !adding()}>
          <p class="text-muted text-center overrides-empty">No overrides configured</p>
        </Show>
      </div>
    </div>
  );
}
