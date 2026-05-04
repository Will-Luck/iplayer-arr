import type { JSX, Component } from "solid-js";
import { Icon, type IconName } from "./icons";

export type IconButtonTone = "default" | "danger" | "primary";
export type IconButtonSize = "sm" | "md";

const TONES: Record<IconButtonTone, string> = {
  default: "text-text-secondary hover:bg-elevated hover:text-text-primary",
  danger: "text-text-secondary hover:bg-danger/10 hover:text-danger",
  primary: "text-accent hover:bg-accent-muted",
};

const SIZES: Record<IconButtonSize, { box: string; icon: number }> = {
  sm: { box: "h-7 w-7", icon: 14 },
  md: { box: "h-9 w-9", icon: 16 },
};

type Props = {
  icon: IconName;
  "aria-label": string;
  tone?: IconButtonTone;
  size?: IconButtonSize;
  class?: string;
} & JSX.ButtonHTMLAttributes<HTMLButtonElement>;

export const IconButton: Component<Props> = (props) => {
  const tone = () => props.tone ?? "default";
  const size = () => SIZES[props.size ?? "md"];
  return (
    <button
      type={props.type ?? "button"}
      {...props}
      class={`inline-flex items-center justify-center rounded-md transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface disabled:opacity-50 disabled:cursor-not-allowed ${size().box} ${TONES[tone()]} ${props.class ?? ""}`}
    >
      <Icon name={props.icon} size={size().icon} />
    </button>
  );
};
