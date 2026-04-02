import { createSignal } from "solid-js";

export interface Toast {
  id: number;
  type: "success" | "error" | "warning";
  message: string;
}

const [toasts, setToasts] = createSignal<Toast[]>([]);
let nextId = 0;

export function addToast(type: Toast["type"], message: string) {
  const id = nextId++;
  const timeout = type === "error" ? 6000 : 4000;

  setToasts(prev => {
    const next = [...prev, { id, type, message }];
    return next.length > 3 ? next.slice(-3) : next;
  });

  setTimeout(() => removeToast(id), timeout);
}

export function removeToast(id: number) {
  setToasts(prev => prev.filter(t => t.id !== id));
}

export { toasts };
