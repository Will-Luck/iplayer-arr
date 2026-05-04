import type { Component } from "solid-js";
import { For } from "solid-js";
import { toasts, removeToast } from "../toast";

const TONE: Record<string, string> = {
  success: "bg-success/15 text-success border-success/40",
  error: "bg-danger/15 text-danger border-danger/40",
  info: "bg-info/15 text-info border-info/40",
  warning: "bg-warning/15 text-warning border-warning/40",
};

export const ToastViewport: Component = () => {
  return (
    <div
      class="fixed bottom-4 right-4 z-[100] flex flex-col gap-2"
      aria-live="polite"
      aria-atomic="false"
    >
      <For each={toasts()}>
        {(t) => (
          <button
            type="button"
            onClick={() => removeToast(t.id)}
            class={`pointer-events-auto rounded-md border px-4 py-2.5 text-sm font-medium shadow-lg backdrop-blur-sm transition-all ${TONE[t.type] ?? TONE.info}`}
            role={t.type === "error" ? "alert" : "status"}
          >
            {t.message}
          </button>
        )}
      </For>
    </div>
  );
};

export default ToastViewport;
