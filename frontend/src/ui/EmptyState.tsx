import type { Component, JSX } from "solid-js";
import { Show } from "solid-js";
import { Icon, type IconName } from "./icons";

type Props = {
  icon?: IconName;
  title?: JSX.Element;
  description?: JSX.Element;
  action?: JSX.Element;
  class?: string;
};

export const EmptyState: Component<Props> = (props) => (
  <div
    class={`flex flex-col items-center justify-center gap-3 py-10 text-center text-text-secondary ${props.class ?? ""}`}
  >
    <Show when={props.icon}>
      <span class="rounded-full bg-elevated p-3 text-text-secondary">
        <Icon name={props.icon!} size={20} />
      </span>
    </Show>
    <Show when={props.title}>
      <p class="text-sm font-medium text-text-primary">{props.title}</p>
    </Show>
    <Show when={props.description}>
      <p class="max-w-sm text-xs text-text-secondary">{props.description}</p>
    </Show>
    <Show when={props.action}>
      <div class="mt-2">{props.action}</div>
    </Show>
  </div>
);
