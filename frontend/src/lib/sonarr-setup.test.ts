// @vitest-environment node
import { describe, expect, it } from "vitest";
import { getSonarrSetup } from "./sonarr-setup";

describe("getSonarrSetup", () => {
  it("builds concrete Sonarr values from the current browser location", () => {
    expect(
      getSonarrSetup({
        origin: "http://192.0.2.10:62001",
        hostname: "192.0.2.10",
        port: "62001",
        protocol: "http:",
      }),
    ).toEqual({
      indexerUrl: "http://192.0.2.10:62001/newznab/api",
      sabHost: "192.0.2.10",
      sabPort: "62001",
      sabBase: "/sabnzbd",
      sabCategory: "sonarr",
    });
  });

  it("falls back to the protocol default port when the location omits one", () => {
    expect(
      getSonarrSetup({
        origin: "https://iplayer-arr.example",
        hostname: "iplayer-arr.example",
        port: "",
        protocol: "https:",
      }),
    ).toEqual({
      indexerUrl: "https://iplayer-arr.example/newznab/api",
      sabHost: "iplayer-arr.example",
      sabPort: "443",
      sabBase: "/sabnzbd",
      sabCategory: "sonarr",
    });
  });
});
