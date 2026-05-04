import type { Component, JSX, ParentComponent } from "solid-js";
import { Show } from "solid-js";

type DivProps = JSX.HTMLAttributes<HTMLDivElement>;

type CardProps = DivProps & {
  class?: string;
};

const cls = (base: string, extra?: string) => (extra ? `${base} ${extra}` : base);

const CardRoot: ParentComponent<CardProps> = (props) => (
  <div
    {...props}
    class={cls(
      "rounded-lg border border-border bg-surface text-text-primary",
      props.class,
    )}
  >
    {props.children}
  </div>
);

type HeaderProps = DivProps & {
  title?: JSX.Element;
  actions?: JSX.Element;
  class?: string;
};

const Header: Component<HeaderProps> = (props) => {
  const localProps = props as HeaderProps;
  return (
    <div
      class={cls(
        "flex items-center justify-between gap-3 border-b border-border px-4 py-3",
        localProps.class,
      )}
    >
      <Show
        when={localProps.title}
        fallback={localProps.children as unknown as JSX.Element}
      >
        <h2 class="text-xs font-semibold uppercase tracking-[0.08em] text-text-secondary">
          {localProps.title}
        </h2>
      </Show>
      <Show when={localProps.actions}>
        <div class="ml-auto flex items-center gap-2">{localProps.actions}</div>
      </Show>
    </div>
  );
};

type BodyProps = DivProps & {
  padded?: boolean;
  class?: string;
};

const Body: ParentComponent<BodyProps> = (props) => {
  const padded = props.padded === undefined ? true : props.padded;
  return (
    <div class={cls(padded ? "p-4" : "", props.class)}>{props.children}</div>
  );
};

const Toolbar: ParentComponent<CardProps> = (props) => (
  <div
    class={cls(
      "flex flex-wrap items-center gap-2 border-b border-border px-4 py-2",
      props.class,
    )}
  >
    {props.children}
  </div>
);

const Footer: ParentComponent<CardProps> = (props) => (
  <div
    class={cls(
      "flex items-center justify-between gap-3 border-t border-border px-4 py-3 text-sm text-text-secondary",
      props.class,
    )}
  >
    {props.children}
  </div>
);

export const Card = Object.assign(CardRoot, {
  Header,
  Body,
  Toolbar,
  Footer,
});
