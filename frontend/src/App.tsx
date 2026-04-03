import { Router, Route } from "@solidjs/router";
import { createSignal, onMount, onCleanup } from "solid-js";
import Nav from "./components/Nav";
import ToastContainer from "./components/Toast";
import SetupWizard from "./components/SetupWizard";
import Dashboard from "./pages/Dashboard";
import Downloads from "./pages/Downloads";
import Search from "./pages/Search";
import Config from "./pages/Config";
import Overrides from "./pages/Overrides";
import Logs from "./pages/Logs";
import System from "./pages/System";
import { api } from "./api";

function Layout(props: { children?: any }) {
  const [showWizard, setShowWizard] = createSignal(false);

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
      <Nav />
      <main class="main">{props.children}</main>
      <ToastContainer />
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
    </Router>
  );
}
