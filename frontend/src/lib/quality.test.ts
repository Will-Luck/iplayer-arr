// @vitest-environment node
import { describe, expect, it } from "vitest";
import { QUALITY_OPTIONS } from "../types";
import { chosenQuality, defaultQuality, qualityOptionsFor } from "./quality";

// Gitea #89: the Search page offered 1080p from a static list even when
// the prober had verified that BBC serves the episode at 720p at most.
describe("qualityOptionsFor", () => {
  it("offers every rung when the probe verified all of them", () => {
    expect(qualityOptionsFor([1080, 720, 540, 396])).toEqual(["1080p", "720p", "540p", "396p"]);
  });

  it("drops 1080p when the probe verified 720p and below", () => {
    expect(qualityOptionsFor([720, 540, 396])).toEqual(["720p", "540p", "396p"]);
  });

  it("falls back to the full static list when nothing was learned", () => {
    // Absent key (older server, unprobed, past the probe cap), and the
    // two shapes a hand-written client might send instead.
    expect(qualityOptionsFor(undefined)).toEqual([...QUALITY_OPTIONS]);
    expect(qualityOptionsFor(null)).toEqual([...QUALITY_OPTIONS]);
    expect(qualityOptionsFor([])).toEqual([...QUALITY_OPTIONS]);
  });

  it("dedupes and orders unsorted heights highest first", () => {
    expect(qualityOptionsFor([720, 1080, 720])).toEqual(["1080p", "720p"]);
    expect(qualityOptionsFor([396, 540])).toEqual(["540p", "396p"]);
  });

  it("puts an in-between height on the rung at or below it", () => {
    expect(qualityOptionsFor([576, 396])).toEqual(["540p", "396p"]);
    expect(qualityOptionsFor([540])).toEqual(["540p"]);
    expect(qualityOptionsFor([539])).toEqual(["396p"]);
    expect(qualityOptionsFor([2160])).toEqual(["1080p"]);
  });

  it("offers only the lowest rung when every verified height is below it", () => {
    expect(qualityOptionsFor([288])).toEqual(["396p"]);
    expect(qualityOptionsFor([395, 0, -1])).toEqual(["396p"]);
  });

  it("falls back to the full list when no positive height was verified", () => {
    expect(qualityOptionsFor([0, -1])).toEqual([...QUALITY_OPTIONS]);
  });

  it("never hands out the shared static list itself", () => {
    const first = qualityOptionsFor(undefined);
    first.pop();
    expect(qualityOptionsFor(undefined)).toHaveLength(QUALITY_OPTIONS.length);
  });
});

describe("defaultQuality", () => {
  it("keeps the long-standing 720p default when it is offered", () => {
    expect(defaultQuality(["1080p", "720p", "540p", "396p"])).toBe("720p");
    expect(defaultQuality(["720p", "540p", "396p"])).toBe("720p");
  });

  it("picks the highest offered quality when 720p is not offered", () => {
    expect(defaultQuality(["540p", "396p"])).toBe("540p");
    expect(defaultQuality(["1080p"])).toBe("1080p");
  });
});

describe("chosenQuality", () => {
  it("keeps the user's pick while it is still offered", () => {
    expect(chosenQuality(["1080p", "720p"], "1080p")).toBe("1080p");
    expect(chosenQuality(["720p", "540p", "396p"], "396p")).toBe("396p");
  });

  it("replaces a pick the current options no longer offer", () => {
    // Picked 1080p on an earlier search; the same PID now came back
    // verified at 720p and below.
    expect(chosenQuality(["720p", "540p", "396p"], "1080p")).toBe("720p");
    expect(chosenQuality(["540p", "396p"], "720p")).toBe("540p");
  });

  it("uses the default when nothing was picked", () => {
    expect(chosenQuality(["540p", "396p"], undefined)).toBe("540p");
    expect(chosenQuality(["1080p", "720p", "540p", "396p"], "")).toBe("720p");
  });
});
