import { A } from "@solidjs/router";
import logoAppIcon from "../assets/logo-app-icon.png";
import logoMark from "../assets/logo-mark.png";

type BrandProps = {
  collapsed?: boolean;
};

export default function Brand(props: BrandProps) {
  const collapsed = () => !!props.collapsed;

  return (
    <A
      href="/"
      class="nav-brand"
      classList={{ collapsed: collapsed() }}
      aria-label="iplayer-arr home"
    >
      <img
        class="nav-brand-icon"
        classList={{ "nav-brand-icon-square": collapsed() }}
        src={collapsed() ? logoAppIcon : logoMark}
        alt=""
      />
      <span class="nav-brand-wordmark">iplayer-arr</span>
    </A>
  );
}
