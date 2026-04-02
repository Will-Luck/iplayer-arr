import { createSignal, onMount, For, Show } from "solid-js";
import type { ShowOverride } from "../types";
import { api } from "../api";

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
    await api.putOverride(o);
    setOverrides(await api.listOverrides());
    setEditing(null);
    setAdding(false);
  }

  async function remove(show: string) {
    if (!confirm(`Delete override for "${show}"?`)) return;
    await api.deleteOverride(show);
    setOverrides(await api.listOverrides());
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
        <input class="input" value={draft().show_name} onInput={e => { updateDraft("show_name", e.target.value); setNameError(""); }} disabled={!!editing()} style="min-width:140px" />
        <Show when={nameError()}>
          <p class="text-danger" style="font-size:11px;margin-top:2px">{nameError()}</p>
        </Show>
      </td>
      <td style="text-align:center"><input type="checkbox" checked={draft().force_date_based} onChange={e => updateDraft("force_date_based", e.target.checked)} /></td>
      <td><input class="input" type="number" value={draft().force_series_num} onInput={e => updateDraft("force_series_num", +e.target.value)} style="width:64px" /></td>
      <td><input class="input" type="number" value={draft().series_offset} onInput={e => updateDraft("series_offset", +e.target.value)} style="width:64px" /></td>
      <td><input class="input" type="number" value={draft().episode_offset} onInput={e => updateDraft("episode_offset", +e.target.value)} style="width:64px" /></td>
      <td><input class="input" value={draft().custom_name} onInput={e => updateDraft("custom_name", e.target.value)} style="min-width:120px" /></td>
      <td>
        <div style="display:flex;gap:4px">
          <button class="btn btn-primary btn-sm" onClick={save}>Save</button>
          <button class="btn btn-sm" style="background:var(--bg-input);color:var(--text-secondary)" onClick={() => { setEditing(null); setAdding(false); }}>Cancel</button>
        </div>
      </td>
    </tr>
  );

  return (
    <div class="card">
      <div class="card-header" style="display:flex;justify-content:space-between;align-items:center">
        <span>Show Overrides</span>
        <button class="btn btn-primary btn-sm" onClick={startAdd} disabled={adding()}>Add Override</button>
      </div>
      <div class="card-body" style="overflow-x:auto">
        <p class="text-muted" style="font-size:12px;margin-bottom:12px">
          Override how specific shows are numbered. Force date-based for daily shows, adjust series/episode offsets for mismatched numbering.
        </p>
        <table class="table">
          <thead>
            <tr>
              <th>Show Name</th>
              <th>Date-Based</th>
              <th>Force Series</th>
              <th>Series Offset</th>
              <th>Ep Offset</th>
              <th>Custom Name</th>
              <th>Actions</th>
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
                    <div style="display:flex;gap:4px">
                      <button class="btn btn-sm" style="background:var(--accent);color:#fff" onClick={() => startEdit(o)}>Edit</button>
                      <button class="btn btn-danger btn-sm" onClick={() => remove(o.show_name)}>Delete</button>
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
          <p class="text-muted" style="text-align:center;padding:16px 0;font-size:13px">No overrides configured</p>
        </Show>
      </div>
    </div>
  );
}
