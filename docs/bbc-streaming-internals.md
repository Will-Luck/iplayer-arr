# BBC streaming internals

Implementation reference for the BBC streaming layer. Technical description only, not a user guide.

## Overview

iplayer-arr fetches BBC media via a two-stage resolution chain:

1. **IBL search** (`internal/bbc/ibl.go`) queries BBC's public programme index to find matching brands and episodes.
2. **Mediaselector resolution** (`internal/bbc/mediaselector.go`) exchanges a media version ID (VPID) for a set of stream URLs spanning multiple bitrates and delivery formats.

Once a stream URL is selected, the downloader in `internal/download/ffmpeg.go` passes it to ffmpeg for segment-level retrieval.

## HLS variant structure

BBC's HLS master playlists list variant streams, each tagged with a `BANDWIDTH` attribute and a URL containing a `video=<bitrate>` query parameter. The variant with the highest `BANDWIDTH` attribute is normally selected by the downloader.

The `QualityProber` in `internal/bbc/prober.go` inspects the master playlist at search time to determine which qualities are available for each programme. Results are cached per-PID in the BoltDB `quality_cache` bucket (see `internal/store/quality_cache.go`).

## Higher-bitrate variants

Some BBC HLS master playlists reference variants up to `video=5000000` (approximately 720p). Higher-bitrate content is served from the same CDN at `video=12000000` (approximately 1080p). `Client.ProbeHiddenFHD` in `internal/bbc/fhdprobe.go` HEAD-probes the `video=12000000` URL form and reports whether that variant is retrievable for a given master playlist. When it is, the downloader's `resolveHLSVariant` (`internal/download/ffmpeg.go`) uses the higher-bitrate URL in place of the highest-listed variant.

## DASH streams

BBC also serves content via DASH (`.mpd` manifests) for devices that do not use HLS. iplayer-arr does not currently use DASH streams - HLS is preferred because ffmpeg handles HLS variant URLs directly. DASH support would require a separate manifest parser.

## DRM

Some premium BBC content (certain films, occasional drama series) uses Widevine DRM. Widevine-protected streams cannot be retrieved by iplayer-arr and are skipped during IBL filtering.

## Implementation files

| Concern | File |
|---|---|
| IBL search / episode listing | `internal/bbc/ibl.go` |
| Mediaselector (VPID -> streams) | `internal/bbc/mediaselector.go` |
| HLS master playlist + variant probing | `internal/bbc/fhdprobe.go` |
| Quality detection / cache | `internal/bbc/prober.go` |
| BoltDB quality cache | `internal/store/quality_cache.go` |
| ffmpeg invocation | `internal/download/ffmpeg.go` |
