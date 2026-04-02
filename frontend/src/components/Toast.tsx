import { For } from "solid-js";
import { toasts, removeToast } from "../toast";

export default function ToastContainer() {
  return (
    <div class="toast-container">
      <For each={toasts()}>
        {t => (
          <div
            class={`toast toast-${t.type}`}
            onClick={() => removeToast(t.id)}
            role="alert"
          >
            {t.message}
          </div>
        )}
      </For>
    </div>
  );
}
