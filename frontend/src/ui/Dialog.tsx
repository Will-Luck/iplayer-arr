import type { Component, JSX } from "solid-js";
import { Show } from "solid-js";
import { Dialog as KDialog } from "@kobalte/core/dialog";
import { Button } from "./Button";

type DialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title?: JSX.Element;
  description?: JSX.Element;
  children?: JSX.Element;
};

const Root: Component<DialogProps> = (props) => (
  <KDialog open={props.open} onOpenChange={props.onOpenChange}>
    <KDialog.Portal>
      <KDialog.Overlay class="fixed inset-0 z-40 bg-black/60 backdrop-blur-sm data-[expanded]:animate-in data-[closed]:animate-out" />
      <div class="fixed inset-0 z-50 flex items-center justify-center p-4">
        <KDialog.Content class="w-full max-w-md rounded-lg border border-border bg-surface p-5 text-text-primary shadow-2xl focus-visible:outline-none">
          <Show when={props.title}>
            <KDialog.Title class="mb-2 text-base font-semibold">
              {props.title}
            </KDialog.Title>
          </Show>
          <Show when={props.description}>
            <KDialog.Description class="mb-4 text-sm text-text-secondary">
              {props.description}
            </KDialog.Description>
          </Show>
          {props.children}
        </KDialog.Content>
      </div>
    </KDialog.Portal>
  </KDialog>
);

type ConfirmProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title?: JSX.Element;
  message: JSX.Element;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel?: () => void;
};

const Confirm: Component<ConfirmProps> = (props) => (
  <Root
    open={props.open}
    onOpenChange={props.onOpenChange}
    title={props.title}
    description={props.message}
  >
    <div class="mt-5 flex items-center justify-end gap-2">
      <Button
        variant="secondary"
        size="sm"
        onClick={() => {
          props.onCancel?.();
          props.onOpenChange(false);
        }}
      >
        {props.cancelLabel ?? "Cancel"}
      </Button>
      <Button
        variant={props.danger ? "danger" : "primary"}
        size="sm"
        onClick={() => {
          props.onConfirm();
          props.onOpenChange(false);
        }}
      >
        {props.confirmLabel ?? "Confirm"}
      </Button>
    </div>
  </Root>
);

export const Dialog = Object.assign(Root, { Confirm });
