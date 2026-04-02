import { Router, Route } from "@solidjs/router";
import Nav from "./components/Nav";
import ToastContainer from "./components/Toast";
import Dashboard from "./pages/Dashboard";
import Search from "./pages/Search";
import Config from "./pages/Config";
import Overrides from "./pages/Overrides";

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
      <Route path="/search" component={Search} />
      <Route path="/config" component={Config} />
      <Route path="/overrides" component={Overrides} />
    </Router>
  );
}
