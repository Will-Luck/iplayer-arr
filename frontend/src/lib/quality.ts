import { QUALITY_OPTIONS } from "../types";

export type Quality = (typeof QUALITY_OPTIONS)[number];

// Each rung of the static ladder with its pixel height, highest first
// (QUALITY_OPTIONS order).
const RUNGS: { quality: Quality; height: number }[] = QUALITY_OPTIONS.map((q) => ({
  quality: q,
  height: Number.parseInt(q, 10),
}));

// qualityOptionsFor returns the qualities the Search page offers for one
// result. When /api/search sent the heights the prober verified, each is
// put on the highest rung at or below it (the indexer's heightsToTags
// buckets, so the web UI and Sonarr see the same set), duplicates are
// dropped and the order is highest first. With no heights (not probed,
// past the probe cap, probe timed out, an older server), the full
// static list is offered as before. Heights that all sit below the lowest
// rung offer only the lowest rung.
// Gitea #89.
export function qualityOptionsFor(heights: readonly number[] | null | undefined): Quality[] {
  const offered = new Set<Quality>();
  for (const h of heights ?? []) {
    const rung = RUNGS.find((r) => h >= r.height);
    if (rung) offered.add(rung.quality);
  }
  if (offered.size === 0) {
    // Verified heights that all sit below the lowest rung: the worker
    // steps to the closest stream, so offer only that rung.
    if ((heights ?? []).some((h) => h > 0)) return [QUALITY_OPTIONS[QUALITY_OPTIONS.length - 1]];
    return [...QUALITY_OPTIONS];
  }
  return QUALITY_OPTIONS.filter((q) => offered.has(q));
}

// defaultQuality is the quality pre-selected for a result: 720p when it
// is offered (the long-standing default), else the highest one offered.
export function defaultQuality(options: readonly Quality[]): Quality {
  return options.includes("720p") ? "720p" : options[0];
}

// chosenQuality keeps the user's pick only while the result still offers
// it, so a download never asks for a quality the dropdown does not show.
export function chosenQuality(options: readonly Quality[], picked: string | undefined): Quality {
  return options.find((o) => o === picked) ?? defaultQuality(options);
}
