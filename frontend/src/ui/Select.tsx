import type { Component } from "solid-js";
import { Select as KSelect } from "@kobalte/core/select";
import { Icon } from "./icons";

export type SelectOption<T extends string = string> = {
  value: T;
  label: string;
};

type Props<T extends string = string> = {
  value: T;
  onChange: (value: T) => void;
  options: SelectOption<T>[];
  placeholder?: string;
  ariaLabel?: string;
  class?: string;
};

export const Select: Component<Props> = (props) => {
  return (
    <KSelect
      value={props.options.find((o) => o.value === props.value) ?? null}
      onChange={(opt) => {
        if (opt) props.onChange(opt.value);
      }}
      options={props.options}
      optionValue="value"
      optionTextValue="label"
      itemComponent={(itemProps) => (
        <KSelect.Item
          item={itemProps.item}
          class="flex cursor-pointer select-none items-center justify-between rounded px-3 py-2 text-sm text-text-primary hover:bg-raised data-[selected]:bg-accent-muted data-[selected]:text-text-primary"
        >
          <KSelect.ItemLabel>{itemProps.item.rawValue.label}</KSelect.ItemLabel>
          <KSelect.ItemIndicator>
            <Icon name="check" size={14} />
          </KSelect.ItemIndicator>
        </KSelect.Item>
      )}
    >
      <KSelect.Trigger
        aria-label={props.ariaLabel}
        class={`inline-flex h-9 items-center gap-2 rounded-md border border-border bg-elevated px-3 text-sm text-text-primary transition-colors hover:bg-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface ${props.class ?? ""}`}
      >
        <KSelect.Value<SelectOption>>
          {(state) => state.selectedOption()?.label ?? props.placeholder ?? ""}
        </KSelect.Value>
        <KSelect.Icon class="text-text-secondary">
          <Icon name="chevron-down" size={14} />
        </KSelect.Icon>
      </KSelect.Trigger>
      <KSelect.Portal>
        <KSelect.Content class="z-50 min-w-[var(--kb-popper-anchor-width)] overflow-hidden rounded-md border border-border bg-surface shadow-xl">
          <KSelect.Listbox class="max-h-[18rem] overflow-auto p-1" />
        </KSelect.Content>
      </KSelect.Portal>
    </KSelect>
  );
};
