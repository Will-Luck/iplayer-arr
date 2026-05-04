import { Router, Route, useLocation } from "@solidjs/router";
import { createSignal, createEffect, onMount, onCleanup, ErrorBoundary } from "solid-js";
import Nav from "./components/Nav";
import ToastContainer from "./components/Toast";
import ConfirmDialog from "./components/ConfirmDialog";
import SetupWizard from "./components/SetupWizard";
import Dashboard from "./pages/Dashboard";
import Downloads from "./pages/Downloads";
import Search from "./pages/Search";
import Config from "./pages/Config";
import Overrides from "./pages/Overrides";
import Logs from "./pages/Logs";
import System from "./pages/System";
import NotFound from "./pages/NotFound";
import { api } from "./api";

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

  onMount(async () => {
    try {
      const config = await api.getConfig();
      if (!config.api_key) setShowWizard(true);
    } catch {
      setShowWizard(true);
    }

    const handler = () => setShowWizard(true);
    window.addEventListener("rerun-wizard", handler);
    onCleanup(() => window.removeEventListener("rerun-wizard", handler));
  });

  return (
    <div class="layout">
      <a href="#main" class="skip-link">Skip to main content</a>
      <Nav />
      <main ref={mainRef} id="main" class="main" tabindex="-1">
        <ErrorBoundary
          fallback={(err: Error, reset: () => void) => (
            <div class="card" role="alert">
              <div class="card-header">Something went wrong</div>
              <div class="card-body">
                <p class="text-secondary" style="margin-bottom:12px">{String(err?.message ?? err)}</p>
                <button class="btn btn-primary" onClick={reset}>Try again</button>
              </div>
            </div>
          )}
        >
          {props.children}
        </ErrorBoundary>
      </main>
      <ToastContainer />
      <ConfirmDialog />
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
