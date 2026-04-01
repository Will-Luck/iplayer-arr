import { Router, Route } from "@solidjs/router";
import Nav from "./components/Nav";
import Dashboard from "./pages/Dashboard";

function SearchPage() {
  return (
    <div>
      <h1 class="page-title">Search</h1>
      <div class="card"><div class="card-empty">Coming soon</div></div>
    </div>
  );
}

function ConfigPage() {
  return (
    <div>
      <h1 class="page-title">Config</h1>
      <div class="card"><div class="card-empty">Coming soon</div></div>
    </div>
  );
}

function OverridesPage() {
  return (
    <div>
      <h1 class="page-title">Overrides</h1>
      <div class="card"><div class="card-empty">Coming soon</div></div>
    </div>
  );
}

function Layout(props: { children?: any }) {
  return (
    <div class="layout">
      <Nav />
      <main class="main">{props.children}</main>
    </div>
  );
}

export default function App() {
  return (
    <Router root={Layout}>
      <Route path="/" component={Dashboard} />
      <Route path="/search" component={SearchPage} />
      <Route path="/config" component={ConfigPage} />
      <Route path="/overrides" component={OverridesPage} />
    </Router>
  );
}
