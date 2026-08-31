import { Router, Route, useLocation } from "@solidjs/router";
import { createSignal, createEffect, onMount, onCleanup, ErrorBoundary } from "solid-js";
import Nav from "./components/Nav";
import { ToastViewport } from "./ui/Toast";
import { Dialog } from "./ui/Dialog";
import { Card } from "./ui/Card";
import { Button } from "./ui/Button";
import { pending, resolvePending } from "./confirm";
import SetupWizard from "./components/SetupWizard";
import Dashboard from "./pages/Dashboard";
import Downloads from "./pages/Downloads";
import Search from "./pages/Search";
import Config from "./pages/Config";
import Overrides from "./pages/Overrides";
import Logs from "./pages/Logs";
import System from "./pages/System";
import NotFound from "./pages/NotFound";
import { apiKey, UNAUTHORIZED_EVENT } from "./apikey";

function ConfirmHost() {
  return (
    <Dialog.Confirm
      open={pending() !== null}
      onOpenChange={(open) => {
        if (!open && pending()) resolvePending(false);
      }}
      title={pending()?.title}
      message={pending()?.message ?? ""}
      confirmLabel={pending()?.confirmLabel}
      cancelLabel={pending()?.cancelLabel}
      danger={pending()?.danger}
      onConfirm={() => resolvePending(true)}
      onCancel={() => resolvePending(false)}
    />
  );
}

function Layout(props: { children?: any }) {
  const [showWizard, setShowWizard] = createSignal(false);
  const location = useLocation();
  let mainRef: HTMLElement | undefined;
  let firstNav = true;

  createEffect(() => {
    location.pathname;
    if (firstNav) { firstNav = false; return; }
    mainRef?.focus({ preventScroll: false });
  });

  onMount(() => {
    // No key in this browser: the operator has either just installed
    // iplayer-arr or has upgraded past the point where the dashboard
    // became authenticated. Either way the wizard's first step tells
    // them where to find the key.
    if (!apiKey()) setShowWizard(true);

    // A stored key that the server rejects produces the same outcome.
    // api.ts raises this event on any 401 so a stale key cannot leave
    // the operator staring at an empty dashboard with no way back.
    const handler = () => setShowWizard(true);
    window.addEventListener("rerun-wizard", handler);
    window.addEventListener(UNAUTHORIZED_EVENT, handler);
    onCleanup(() => {
      window.removeEventListener("rerun-wizard", handler);
      window.removeEventListener(UNAUTHORIZED_EVENT, handler);
    });
  });

  return (
    <div class="layout">
      <a href="#main" class="skip-link">Skip to main content</a>
      <Nav />
      <main ref={mainRef} id="main" class="main" tabindex="-1">
        <ErrorBoundary
          fallback={(err: Error, reset: () => void) => (
            <Card>
              <Card.Header>Something went wrong</Card.Header>
              <Card.Body>
                <p class="mb-3 text-sm text-text-secondary">
                  {String(err?.message ?? err)}
                </p>
                <Button onClick={reset}>Try again</Button>
              </Card.Body>
            </Card>
          )}
        >
          {props.children}
        </ErrorBoundary>
      </main>
      <ToastViewport />
      <ConfirmHost />
      <SetupWizard show={showWizard()} onComplete={() => setShowWizard(false)} />
    </div>
  );
}

export default function App() {
  return (
    <Router root={Layout}>
      <Route path="/" component={Dashboard} />
      <Route path="/downloads" component={Downloads} />
      <Route path="/search" component={Search} />
      <Route path="/config" component={Config} />
      <Route path="/overrides" component={Overrides} />
      <Route path="/logs" component={Logs} />
      <Route path="/system" component={System} />
      <Route path="*" component={NotFound} />
    </Router>
  );
}
