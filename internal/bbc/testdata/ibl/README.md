# iBL and BBC fixtures (GitHub #57)

Real responses captured on 2026-10-22 from a UK connection. The JSON and XML
files are byte-for-byte copies; nothing has been edited.

| File | Source | What it pins |
|---|---|---|
| `episode_m0033jgh.json` | `GET https://ibl.api.bbci.co.uk/ibl/v1/episodes/m0033jgh` | the #57 programme: one `original` version, `m0033jgj`, PT3M4.874666S |
| `episode_m00336jr.json` | `.../episodes/m00336jr` | Match of the Day, long programme |
| `episode_m00336w8.json` | `.../episodes/m00336w8` | EastEnders: `original` first, `audio-described` second |
| `episode_b00tn579.json` | `.../episodes/b00tn579` | Waterloo Road: `original`, `audio-described`, `signed` |
| `episode_b0074t6w.json` | `.../episodes/b0074t6w` | film, `editorial` version only |
| `episode_m001wsyw.json` | `.../episodes/m001wsyw` | `shortened` version first |
| `episode_b007xh8v.json` | `.../episodes/b007xh8v` | film, no subtitle |
| `episode_m0033d93.json` | `.../episodes/m0033d93` | `editorial` (from 11:45Z) listed before `original` (from 21:45Z) |
| `episode_m00334lv.json` | `.../episodes/m00334lv` | `live: true`, `simulcast` version only |
| `episode_p0p77xg7.json` | `.../episodes/p0p77xg7` | `live: true`, 24-hour `webcast` pop-up channel |
| `episode_m0032bdk.json` | `.../episodes/m0032bdk` | `signed` version only |
| `episode_m002pppv.json` | `.../episodes/m002pppv` | `audio-described` version only (396p) |
| `episode_empty.json` | `.../episodes/zzzzzzzz` | the body iBL returns for an unknown, expired, radio or brand PID |
| `programmes_b007t9y1.json` | `.../programmes/b007t9y1` | valid iBL JSON with no `episodes` key |
| `mediaselector_m0033jgj.xml` | `https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/iptv-all/vpid/m0033jgj/format/xml` | the version id from iBL is accepted as a vpid |
| `mediaselector_m00334lt_after_broadcast.xml` | mediaselector v6 iptv-all for `m00334lt`, minutes after Politics Live ended | the new version served only by `*_live_uk` suppliers at 720p |
| `bbc_error_page.html` | `https://www.bbc.co.uk/error.html` | **trimmed**: the head, title and message of the 29,715-byte page every `/programmes` URL redirected to from 2026-10-21 |
