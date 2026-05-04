import type { JSX, ParentComponent } from "solid-js";

export type BadgeVariant =
  | "completed"
  | "imported"
  | "failed"
  | "pending"
  | "neutral";

const VARIANTS: Record<BadgeVariant, string> = {
  completed:
    "bg-success/15 text-success border border-success/40",
  imported: "bg-info/15 text-info border border-info/40",
  failed: "bg-danger/15 text-danger border border-danger/40",
  pending:
    "bg-warning/15 text-warning border border-warning/40",
  neutral:
    "bg-elevated text-text-secondary border border-border",
};

type Props = {
  variant?: BadgeVariant;
  class?: string;
} & JSX.HTMLAttributes<HTMLSpanElement>;

export const Badge: ParentComponent<Props> = (props) => {
  const variant = () => props.variant ?? "neutral";
  return (
    <span
      {...props}
      class={`inline-flex items-center rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.06em] ${VARIANTS[variant()]} ${props.class ?? ""}`}
    >
      {props.children}
    </span>
  );
};
