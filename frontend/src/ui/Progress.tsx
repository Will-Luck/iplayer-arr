import type { Component, JSX } from "solid-js";
import { Show } from "solid-js";

export type ProgressVariant = "default" | "failed" | "paused";

const FILL: Record<ProgressVariant, string> = {
  default: "bg-accent",
  failed: "bg-danger",
  paused: "bg-warning",
};

type Props = {
  value: number;
  variant?: ProgressVariant;
  label?: JSX.Element;
  showLabel?: boolean;
  class?: string;
  ariaLabel?: string;
};

export const Progress: Component<Props> = (props) => {
  const variant = () => props.variant ?? "default";
  const clamped = () => Math.max(0, Math.min(100, props.value));
  return (
    <div class={`flex items-center gap-3 ${props.class ?? ""}`}>
      <div
        class="relative h-1.5 flex-1 overflow-hidden rounded-full bg-elevated"
        role="progressbar"
        aria-valuenow={Math.round(clamped())}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={props.ariaLabel}
      >
        <div
          class={`h-full rounded-full transition-[width] duration-200 ${FILL[variant()]}`}
          style={{ width: `${clamped()}%` }}
        />
      </div>
      <Show when={props.showLabel ?? props.label !== undefined}>
        <span class="tabular text-xs text-text-secondary min-w-[3ch] text-right">
          {props.label ?? `${clamped().toFixed(1)}%`}
        </span>
      </Show>
    </div>
  );
};
