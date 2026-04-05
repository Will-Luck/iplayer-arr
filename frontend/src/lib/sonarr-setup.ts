export type BrowserLocationLike = Pick<Location, "origin" | "hostname" | "port" | "protocol">;

export type SonarrSetup = {
  indexerUrl: string;
  sabHost: string;
  sabPort: string;
  sabBase: string;
  sabCategory: string;
};

export function getSonarrSetup(location: BrowserLocationLike): SonarrSetup {
  const port = location.port || (location.protocol === "https:" ? "443" : "80");

  return {
    indexerUrl: `${location.origin}/newznab/api`,
    sabHost: location.hostname,
    sabPort: port,
    sabBase: "/sabnzbd",
    sabCategory: "sonarr",
  };
}
