# GitHub #60 fixtures

Real responses captured from the UK on 2026-10-24, between 15:35 and
16:02 UTC, from:

- the iBL API at `https://ibl.api.bbci.co.uk/ibl/v1` (`defaultIBLBase` in
  `internal/bbc/ibl.go`):
  `new-search?q=<query>&rights=web&page=1&per_page=20` and
  `programmes/<pid>/episodes?per_page=200&page=<n>`;
- Skyhook at `https://skyhook.sonarr.tv/v1/tvdb/shows/en/<tvdbid>`.

The search queries are the Skyhook titles Sonarr's tvdbid searches resolve
to: "Porridge" (76578), "Doctor Who (2005)" (78804) and "Doctor Who (2023)"
(449991).

## How they were trimmed

1. Search files keep only the brand hits whose title matches the searched
   name (`nameMatches`), in iBL's order. The other hits are other
   programmes.
2. Episode files keep every episode of Porridge (`b006m9kn`), Doctor Who
   (2005-2022) (`b006q2x0`) and Doctor Who (`p0gglvqn`), with both pages of
   each listing merged into one. The other-era and companion brands
   (`p05dsmwl` Porridge (2017), `p0ggwr8l` Doctor Who (1963-1996),
   `m001sx3r` Doctor Who: Unleashed) keep their first two episodes, enough
   to show the searches drop them.
3. Elements keep `id`, `type`, `title`, `subtitle`, `release_date`,
   `parent_position` and `tleo_id`: what titles and numbering come from.
   Durations and availability dates are dropped, so sizes and pubDates
   are the fallbacks.
4. Skyhook files keep `title`, `firstAired` and, for every episode,
   `seasonNumber`, `episodeNumber`, `title` and `airDate`. No episode was
   dropped: the matcher has to cope with every title TheTVDB has.
5. `count` in each episodes file is the number of elements kept, so
   `ListEpisodesCtx` stops after page 1. The JSON is ASCII, one element per
   line; non-ASCII characters (the en dash in "Doctor Who (2005\u20132022)",
   curly apostrophes) are `\u` escapes and decode to the same strings.
