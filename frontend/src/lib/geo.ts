export function geoBadge(
  geo_status?: string,
  geo_ok?: boolean,
): { label: string; ok: boolean; detail: string } {
  switch (geo_status) {
    case "uk_ok":
      return { label: "UK OK", ok: true, detail: "" };
    case "not_uk":
      return {
        label: "Geo-blocked (non-UK exit)",
        ok: false,
        detail: "VPN exit is not in the UK",
      };
    case "dns_failed":
      return {
        label: "DNS error - set VPN_NAMESERVERS",
        ok: false,
        detail: "Name resolution failed; set VPN_NAMESERVERS to a public resolver",
      };
    case "probe_error":
      return {
        label: "Check failed - VPN/connectivity",
        ok: false,
        detail: "Could not reach BBC; check the tunnel",
      };
    default:
      return { label: geo_ok ? "UK OK" : "Blocked", ok: !!geo_ok, detail: "" };
  }
}
