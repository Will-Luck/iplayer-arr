import { Router, Route } from "@solidjs/router";
import Nav from "./components/Nav";
import ToastContainer from "./components/Toast";
import Dashboard from "./pages/Dashboard";
import Downloads from "./pages/Downloads";
import Search from "./pages/Search";
import Config from "./pages/Config";
import Overrides from "./pages/Overrides";

function ComingSoon() {
  return (
    <div class="card">
      <div class="card-body">
        <h2 class="page-title">Coming Soon</h2>
        <p class="text-secondary">This page is under construction.</p>
      </div>
    </div>
  );
}

function Layout(props: { children?: any }) {
  return (
    <div class="layout">
      <Nav />
      <main class="main">{props.children}</main>
      <ToastContainer />
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
      <Route path="/logs" component={ComingSoon} />
      <Route path="/system" component={ComingSoon} />
    </Router>
  );
}
