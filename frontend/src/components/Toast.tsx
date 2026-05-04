import { For } from "solid-js";
import { toasts, removeToast } from "../toast";

export default function ToastContainer() {
  return (
    <div class="toast-container" aria-live="polite" aria-atomic="false">
      <For each={toasts()}>
        {t => (
          <div
            class={`toast toast-${t.type}`}
            onClick={() => removeToast(t.id)}
            role={t.type === "error" ? "alert" : "status"}
          >
            {t.message}
          </div>
        )}
      </For>
    </div>
  );
}
