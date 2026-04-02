# BBC iPlayer Streaming Internals

Research notes from reverse-engineering BBC's streaming infrastructure (2026-04-02).

## Media Selector API

Endpoint: `https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/{mediaset}/vpid/{vpid}/format/{format}`

- v6 is current; v5 returns 410 Gone
- Formats: `json` or `xml`
- Mediasets that return video: `iptv-all`, `pc`, `mobile-phone-main`, `mobile-tablet-main`
- Optional auth: `/atk/{sha1(secret+vpid)}/asn/1/` -- secret is `7dff7671d0c697fedb1d905d9a121719938b92bf` (same as streamlink/get_iplayer)
- Auth doesn't change what streams are returned, but may affect rate limiting

### What the API claims vs reality

The API metadata **lies about resolution**. Every video media element reports `width=1920 height=1080 bitrate=8490` regardless of actual content. The real resolution is only discoverable by fetching the actual manifest.

## HLS Streaming

Master playlist URL pattern:
```
https://vod-hls-uk-live.akamaized.net/usp/auth/vod/piff_abr_full_hd/{hash}-{vpid}/
  vf_{vpid}_{uuid}.ism.hlsv2.ism/iptv_hd_abr_v1_hls_master.m3u8?__gda__={token}
```

Manifest names by mediaset:
- `iptv-all`: `iptv_hd_abr_v1_hls_master.m3u8`
- `pc`: `pc_hd_abr_v2_hls_master.m3u8`

### Listed variants (in manifest)

| Resolution | Bitrate | Frame rate |
|-----------|---------|------------|
| 704x396   | ~1.0 Mbps | 25 fps |
| 704x396   | ~1.8 Mbps | 50 fps |
| 960x540   | ~3.1 Mbps | 50 fps |
| 1280x720  | ~5.5 Mbps | 50 fps |

The `pc` mediaset has even fewer variants (max 540p in HLS).

### Unlisted 1080p variant

BBC hosts an unlisted 1080p variant on the CDN that is NOT in the master playlist. Access it by replacing the `video=` segment in a variant URL:

```
# Listed 720p variant:
vf_{vpid}_{uuid}.ism.hlsv2-audio_eng_1=128000-video=5070000.m3u8

# Unlisted 1080p variant (replace video= value):
vf_{vpid}_{uuid}.ism.hlsv2-audio_eng_1=128000-video=12000000.m3u8
```

- Probe with HTTP HEAD first -- returns 200 if available, content is empty/broken if not
- The CDN returns HTTP 200 for ANY video= value, so you must verify the stream is actually playable
- Resolution: 1920x1080, H.264, ~12 Mbps
- This is the same technique used by get_iplayer (`dvfhd` mode) and community yt-dlp patches

### Bitrate probing results

| video= value | Actual resolution | Codec | Playable |
|-------------|------------------|-------|----------|
| 827000      | 704x396          | H.264 | Yes |
| 1570000     | 704x396 @50fps   | H.264 | Yes |
| 2812000     | 960x540          | H.264 | Yes |
| 5070000     | 1280x720         | H.264 | Yes |
| 8490000     | 1920x1080        | H.264 | Yes (same as 12000000) |
| 12000000    | 1920x1080        | H.264 | Yes |
| 16000000+   | -                | -     | No (empty response) |

## DASH Streaming

Manifest URL pattern: same base path, `iptv_hd_abr_v1_dash_master.mpd`

DASH manifests contain the same listed variants as HLS (max 720p). The unlisted 1080p trick works the same way via segment URL rewriting, but it's simpler to use HLS since ffmpeg handles HLS variant URLs directly.

## 4K / UHD

- Only available on certified L1 Widevine devices (specific smart TVs, Fire TV 4K, Roku)
- Uses HEVC/H.265 encoding
- Resolution: 3840x2160 (or 2560px intermediate tier)
- Bitrate: ~36 Mbps (live), ~24 Mbps (on-demand)
- HDR: HLG (Hybrid Log-Gamma) on supported content
- Not exposed through the public Media Selector API at all
- Requires hardware-backed DRM (Widevine L1) -- cannot be accessed via software clients
- Only select flagship content (nature docs, live sport, some drama)

## Suppliers / CDNs

- `mf_akamai` -- Akamai (primary, most reliable)
- `mf_cloudfront` -- AWS CloudFront
- `mf_bidi` -- BBC's own CDN
- Avoid `vbidi` suppliers (unreliable)

## Subtitles

Available via Media Selector as separate media elements with `kind=captions`. Format is EBU-TT (XML), which we convert to SRT.

## Auth Token (`__gda__`)

- Scoped to the specific manifest name (e.g., `iptv_hd_abr_v1_hls_master.m3u8`)
- Requesting a different manifest name (e.g., `iptv_fhd_abr_v1_hls_master.m3u8`) returns 403
- Segment requests within the same ISM container share the token
- The `video=12000000` trick works because it's a segment-level URL, not a manifest-level one

## Tools comparison

| Tool | Max quality | Method |
|------|-----------|--------|
| yt-dlp (stock) | 720p | Manifest-listed variants only |
| yt-dlp (patched) | 1080p | `video=12000000` URL rewrite |
| get_iplayer | 1080p | Synthesised `dvfhd` mode, same URL rewrite |
| streamlink | 720p | Manifest-listed variants only |
| iplayer-arr | 1080p | Probes `video=12000000` via HEAD, falls back to 720p |

## References

- get_iplayer source: https://github.com/get-iplayer/get_iplayer
- yt-dlp 1080p gist: https://gist.github.com/werid/19d4197147617fbe8e7a439ff9fab885
- Blog post on 1080p technique: https://shkspr.mobi/blog/2021/11/download-1080p-streams-from-iplayer/
- yt-dlp issue #3463: https://github.com/yt-dlp/yt-dlp/issues/3463
