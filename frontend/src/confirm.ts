import { createSignal } from "solid-js";

export type ConfirmRequest = {
  title?: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
};

type Pending = ConfirmRequest & { resolve: (ok: boolean) => void };

const [pending, setPending] = createSignal<Pending | null>(null);

export function confirmDialog(req: ConfirmRequest): Promise<boolean> {
  return new Promise(resolve => setPending({ ...req, resolve }));
}

export function resolvePending(ok: boolean) {
  const p = pending();
  if (!p) return;
  p.resolve(ok);
  setPending(null);
}

export { pending };
