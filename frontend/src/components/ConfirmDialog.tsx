import { Show, onMount, onCleanup, createEffect } from "solid-js";
import { pending, resolvePending } from "../confirm";

export default function ConfirmDialog() {
  let confirmBtn: HTMLButtonElement | undefined;
  let cancelBtn: HTMLButtonElement | undefined;

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      const p = pending();
      if (!p) return;
      if (e.key === "Escape") {
        e.preventDefault();
        resolvePending(false);
      } else if (e.key === "Tab") {
        const focusables = [cancelBtn, confirmBtn].filter(Boolean) as HTMLButtonElement[];
        if (focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      } else if (e.key === "Enter" && document.activeElement?.tagName !== "BUTTON") {
        e.preventDefault();
        resolvePending(true);
      }
    };
    window.addEventListener("keydown", onKey);
    onCleanup(() => window.removeEventListener("keydown", onKey));
  });

  createEffect(() => {
    const p = pending();
    if (!p) return;
    queueMicrotask(() => {
      (p.danger ? cancelBtn : confirmBtn)?.focus();
    });
  });

  return (
    <Show when={pending()}>
      {(p) => (
        <div class="dialog-backdrop" onClick={() => resolvePending(false)}>
          <div
            class="dialog"
            role="alertdialog"
            aria-modal="true"
            aria-labelledby={p().title ? "confirm-title" : undefined}
            aria-describedby="confirm-message"
            onClick={(e) => e.stopPropagation()}
          >
            <Show when={p().title}>
              <h2 id="confirm-title" class="dialog-title">{p().title}</h2>
            </Show>
            <p id="confirm-message" class="dialog-message">{p().message}</p>
            <div class="dialog-actions">
              <button
                ref={cancelBtn}
                type="button"
                class="btn"
                onClick={() => resolvePending(false)}
              >
                {p().cancelLabel ?? "Cancel"}
              </button>
              <button
                ref={confirmBtn}
                type="button"
                class={p().danger ? "btn btn-danger" : "btn btn-primary"}
                onClick={() => resolvePending(true)}
              >
                {p().confirmLabel ?? "Confirm"}
              </button>
            </div>
          </div>
        </div>
      )}
    </Show>
  );
}
