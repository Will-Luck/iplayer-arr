import type { JSX, ParentComponent } from "solid-js";
import { Show } from "solid-js";

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "warning"
  | "link";
export type ButtonSize = "sm" | "md";

const VARIANTS: Record<ButtonVariant, string> = {
  primary:
    "bg-accent text-white hover:bg-accent-hover focus-visible:ring-accent",
  secondary:
    "bg-elevated text-text-primary hover:bg-raised border border-border focus-visible:ring-accent",
  ghost:
    "bg-transparent text-text-primary hover:bg-elevated focus-visible:ring-accent",
  danger:
    "bg-transparent text-danger border border-danger hover:bg-danger hover:text-white focus-visible:ring-danger",
  warning:
    "bg-warning text-white hover:opacity-90 focus-visible:ring-warning",
  link:
    "bg-transparent text-accent hover:underline px-0 py-0 focus-visible:ring-accent",
};

const SIZES: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-xs",
  md: "h-10 px-4 text-sm",
};

type Props = {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  class?: string;
} & JSX.ButtonHTMLAttributes<HTMLButtonElement>;

export const Button: ParentComponent<Props> = (props) => {
  const variant = () => props.variant ?? "primary";
  const size = () => props.size ?? "md";
  return (
    <button
      type={props.type ?? "button"}
      {...props}
      class={`inline-flex items-center justify-center gap-2 rounded-md font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-surface ${VARIANTS[variant()]} ${variant() === "link" ? "" : SIZES[size()]} ${props.class ?? ""}`}
      disabled={props.disabled || props.loading}
      aria-busy={props.loading || undefined}
    >
      <Show when={props.loading}>
        <svg
          class="animate-spin"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-opacity="0.25" stroke-width="3" />
          <path d="M4 12a8 8 0 0 1 8-8" stroke="currentColor" stroke-width="3" stroke-linecap="round" />
        </svg>
      </Show>
      {props.children}
    </button>
  );
};
