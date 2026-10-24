# GitHub #58 fixtures

Real responses captured from the UK on 2026-10-24, between 00:05 and
00:40 UTC, from:

- the iBL API at `https://ibl.api.bbci.co.uk/ibl/v1` (`defaultIBLBase` in
  `internal/bbc/ibl.go`):
  `new-search?q=<query>&rights=web&page=1&per_page=20` and
  `programmes/<pid>/episodes?per_page=200&page=1`;
- Skyhook at `https://skyhook.sonarr.tv/v1/tvdb/shows/en/<tvdbid>`.

`search_*.json` is one search response, `episodes_<pid>.json` is page 1 of
a brand's episode listing and `skyhook_<tvdbid>.json` is one Skyhook show.

## How they were trimmed

1. Only the search hits and episodes the tests use are kept, in iBL's
   order (see the table). A dropped search hit is another programme whose
   title can never match the searched name.
2. Each element keeps only the fields iplayer-arr decodes (`iblElementJSON`
   in `internal/bbc/ibl.go`): `id`, `type`, `title`, `subtitle`,
   `synopses.small`, `images.standard`, `master_brand.titles.small`,
   `release_date`, `parent_position`, `tleo_id`, and `download`,
   `duration.value` and `availability.start` of every version. Skyhook
   files keep `title` and `firstAired` (`lookupTVDBShow`).
3. Kept values are unchanged, with two exceptions. `count` in each
   episodes file is the number of elements kept, so `ListEpisodesCtx`
   stops after page 1. The JSON is rewritten as ASCII, one element per
   line, so non-ASCII characters (the en dash in iBL's
   "Doctor Who (2005\u20132022)", curly apostrophes) appear as `\u`
   escapes; they decode to the same strings.

After trimming, every item the tests emit has the same title, size and
tvdbid attr as the same item emitted from the full captures. This was
checked for every test that reads these files, by running the same tests
over the untrimmed captures: every search hit, every page of each
listing, and the listings of the other brands in the hits (a listing a
test fails on purpose still fails). The full captures emit more items,
as expected, but none of the companion or one-off PIDs the tests rule
out.

| File | Captured from | Kept |
| --- | --- | --- |
| `search_The_Celebrity_Traitors.json` | q=The Celebrity Traitors | m002csng, m002kksc |
| `search_The_Celebrity_Traitors_colon_Uncloaked.json` | q=The Celebrity Traitors: Uncloaked | m002kksc, m002csng |
| `search_The_Traitors.json` | q=The Traitors | p0db9b2t, m001ty6w |
| `search_Talking_Tom_Heroes.json` | q=Talking Tom Heroes | m002lgjh |
| `search_Doctor_Who_2023.json` | q=Doctor Who (2023) | p0gglvqn, b00p3pvl, m001sx3r, p0ggwr8l, b006q2x0, p04fttjc, p0gsx48n, b007zv0y, m001zg9k, b00gfy2c |
| `search_Doctor_Who_2005.json` | q=Doctor Who (2005) | b006q2x0, p0gglvqn, b00p3pvl, m001sx3r, p0ggwr8l, b007zv0y |
| `search_The_Apprentice_UK.json` | q=The Apprentice (UK) | b0071b63, m002qm1x, b007qgcl |
| `search_Gavin_and_Stacey.json` | q=Gavin & Stacey | b007nf70, m0026d9p |
| `search_EastEnders.json` | q=EastEnders | b006m86d, m0025c1l |
| `episodes_m002csng.json` | The Celebrity Traitors | Series 2 episodes 1 to 3 |
| `episodes_m002kksc.json` | The Celebrity Traitors: Uncloaked | Series 2 episodes 1 to 3 |
| `episodes_p0db9b2t.json` | The Traitors | m002pkvf (S04E05) |
| `episodes_m001ty6w.json` | The Traitors: Uncloaked | m002pkvh (S04E05) |
| `episodes_m002lgjh.json` | Talking Tom Heroes: Suddenly Super | m002rw0y (S01E40) |
| `episodes_p0gglvqn.json` | Doctor Who | m0026d24, m0029db8 |
| `episodes_m001sx3r.json` | Doctor Who: Unleashed | m002b1m7 |
| `episodes_b006q2x0.json` | Doctor Who (2005-2022) | m001dhsb, m00123l4, b06kv29n, p02ct985, b0074fll |
| `episodes_p0ggwr8l.json` | Doctor Who (1963-1996) | m001wrtl (count was 662) |
| `episodes_b0071b63.json` | The Apprentice | m001gr7b, m002v3q0 |
| `episodes_b007qgcl.json` | The Apprentice: You're Fired! | m001gr46 |
| `episodes_m002qm1x.json` | The Apprentice: Unfinished Business | m002v46k |
| `episodes_b007nf70.json` | Gavin & Stacey | b00gbjhd, m000csm7, m0026dmr |
| `episodes_m0025c1l.json` | EastEnders: Christmas 1986 - 2007 | b008l3by, b008m8ws, p04cpspm |
| `episodes_b006m86d.json` | EastEnders | m00336w8 (count was 1009) |
| `skyhook_*.json` | TVDB 453660, 468742, 420543, 367318, 449991, 78804, 75795, 80435, 71753 | title, firstAired |
