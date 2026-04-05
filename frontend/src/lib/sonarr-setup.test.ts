// @vitest-environment node
import { describe, expect, it } from "vitest";
import { getSonarrSetup } from "./sonarr-setup";

describe("getSonarrSetup", () => {
  it("builds concrete Sonarr values from the current browser location", () => {
    expect(
      getSonarrSetup({
        origin: "http://192.168.1.57:62932",
        hostname: "192.168.1.57",
        port: "62932",
        protocol: "http:",
      }),
    ).toEqual({
      indexerUrl: "http://192.168.1.57:62932/newznab/api",
      sabHost: "192.168.1.57",
      sabPort: "62932",
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
