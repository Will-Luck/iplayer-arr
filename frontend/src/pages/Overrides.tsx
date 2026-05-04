import { createSignal, onMount, For, Show } from "solid-js";
import type { ShowOverride } from "../types";
import { api } from "../api";
import { addToast } from "../toast";
import { confirmDialog } from "../confirm";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { IconButton } from "../ui/IconButton";
import { Table } from "../ui/Table";
import { EmptyState } from "../ui/EmptyState";
import { Icon } from "../ui/icons";

const TEXT_INPUT_CLASS =
  "h-8 w-full rounded-md border border-border bg-elevated px-2 text-sm text-text-primary placeholder:text-text-tertiary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface disabled:opacity-60";

const emptyOverride = (): ShowOverride => ({
  show_name: "",
  force_date_based: false,
  force_series_num: 0,
  force_position: false,
  series_offset: 0,
  episode_offset: 0,
  custom_name: "",
});

export default function Overrides() {
  const [overrides, setOverrides] = createSignal<ShowOverride[]>([]);
  const [editing, setEditing] = createSignal<string | null>(null);
  const [adding, setAdding] = createSignal(false);
  const [draft, setDraft] = createSignal<ShowOverride>(emptyOverride());
  const [nameError, setNameError] = createSignal("");

  onMount(async () => {
    setOverrides(await api.listOverrides());
  });

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
    const ok = await confirmDialog({
      title: "Delete override?",
      message: `Delete override for "${show}"?`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
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
    setNameError("");
    setAdding(true);
  }

  function cancel() {
    setEditing(null);
    setAdding(false);
    setNameError("");
  }

  function updateDraft(field: keyof ShowOverride, value: string | number | boolean) {
    setDraft((prev) => ({ ...prev, [field]: value }));
  }

  const editRow = () => (
    <Table.TR>
      <Table.TD label="Show name">
        <input
          class={TEXT_INPUT_CLASS}
          value={draft().show_name}
          onInput={(e) => {
            updateDraft("show_name", e.currentTarget.value);
            setNameError("");
          }}
          disabled={!!editing()}
          aria-label="Show name"
          placeholder="Show name"
        />
        <Show when={nameError()}>
          <p class="mt-1 text-xs text-danger">{nameError()}</p>
        </Show>
      </Table.TD>
      <Table.TD align="center" label="Date-based">
        <input
          type="checkbox"
          class="h-4 w-4 rounded border-border bg-elevated accent-accent"
          checked={draft().force_date_based}
          onChange={(e) => updateDraft("force_date_based", e.currentTarget.checked)}
          aria-label="Force date-based"
        />
      </Table.TD>
      <Table.TD label="Force series">
        <input
          class={TEXT_INPUT_CLASS}
          type="number"
          value={draft().force_series_num}
          onInput={(e) => updateDraft("force_series_num", +e.currentTarget.value)}
          aria-label="Force series number"
        />
      </Table.TD>
      <Table.TD label="Series offset">
        <input
          class={TEXT_INPUT_CLASS}
          type="number"
          value={draft().series_offset}
          onInput={(e) => updateDraft("series_offset", +e.currentTarget.value)}
          aria-label="Series offset"
        />
      </Table.TD>
      <Table.TD label="Episode offset">
        <input
          class={TEXT_INPUT_CLASS}
          type="number"
          value={draft().episode_offset}
          onInput={(e) => updateDraft("episode_offset", +e.currentTarget.value)}
          aria-label="Episode offset"
        />
      </Table.TD>
      <Table.TD label="Custom name">
        <input
          class={TEXT_INPUT_CLASS}
          value={draft().custom_name}
          onInput={(e) => updateDraft("custom_name", e.currentTarget.value)}
          aria-label="Custom name"
        />
      </Table.TD>
      <Table.TD align="right" label="Actions">
        <div class="flex items-center justify-end gap-2">
          <Button size="sm" onClick={save}>Save</Button>
          <Button variant="secondary" size="sm" onClick={cancel}>Cancel</Button>
        </div>
      </Table.TD>
    </Table.TR>
  );

  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Overrides</h1>

      <Card>
        <Card.Header
          actions={
            <Button size="sm" onClick={startAdd} disabled={adding()}>
              <Icon name="plus" size={14} />
              Add override
            </Button>
          }
        >
          Show overrides
        </Card.Header>
        <Card.Body>
          <p class="text-sm text-text-secondary">
            Override how specific shows are numbered. Force date-based for daily shows; adjust series and episode offsets for mismatched numbering.
          </p>
        </Card.Body>
        <Card.Body padded={false}>
          <Show
            when={overrides().length > 0 || adding()}
            fallback={
              <EmptyState
                icon="settings"
                title="No overrides configured"
                description="Add an override to customise series, episode, or naming logic for a show."
              />
            }
          >
            <Table collapse="card">
              <caption class="sr-only">Show name overrides</caption>
              <Table.THead>
                <Table.TR>
                  <Table.TH name="show">Show name</Table.TH>
                  <Table.TH name="date-based" align="center" width={110}>Date-based</Table.TH>
                  <Table.TH name="force-series" width={120}>Force series</Table.TH>
                  <Table.TH name="series-offset" width={120}>Series offset</Table.TH>
                  <Table.TH name="ep-offset" width={110}>Ep offset</Table.TH>
                  <Table.TH name="custom-name">Custom name</Table.TH>
                  <Table.TH name="actions" align="right" width={140}>
                    <span class="sr-only">Actions</span>
                  </Table.TH>
                </Table.TR>
              </Table.THead>
              <Table.TBody>
                <Show when={adding()}>{editRow()}</Show>
                <For each={overrides()}>
                  {(o) => (
                    <Show
                      when={editing() === o.show_name}
                      fallback={
                        <Table.TR>
                          <Table.TD primary label="Show name">{o.show_name}</Table.TD>
                          <Table.TD align="center" label="Date-based">
                            <Badge variant={o.force_date_based ? "completed" : "neutral"}>
                              {o.force_date_based ? "Yes" : "No"}
                            </Badge>
                          </Table.TD>
                          <Table.TD muted tabular label="Force series">
                            {o.force_series_num || "-"}
                          </Table.TD>
                          <Table.TD muted tabular label="Series offset">
                            {o.series_offset || "-"}
                          </Table.TD>
                          <Table.TD muted tabular label="Ep offset">
                            {o.episode_offset || "-"}
                          </Table.TD>
                          <Table.TD muted label="Custom name">
                            {o.custom_name || "-"}
                          </Table.TD>
                          <Table.TD align="right" label="Actions">
                            <div class="flex items-center justify-end gap-1">
                              <IconButton
                                icon="settings"
                                tone="primary"
                                size="sm"
                                aria-label={`Edit ${o.show_name}`}
                                onClick={() => startEdit(o)}
                              />
                              <IconButton
                                icon="trash"
                                tone="danger"
                                size="sm"
                                aria-label={`Delete ${o.show_name}`}
                                onClick={() => remove(o.show_name)}
                              />
                            </div>
                          </Table.TD>
                        </Table.TR>
                      }
                    >
                      {editRow()}
                    </Show>
                  )}
                </For>
              </Table.TBody>
            </Table>
          </Show>
        </Card.Body>
      </Card>
    </div>
  );
}
