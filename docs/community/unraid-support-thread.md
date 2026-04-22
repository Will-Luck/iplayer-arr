# Unraid Forum Support Thread

Official support thread post for the Unraid Community forums.

**Target forum:** Unraid Community, Docker Containers (post to General Support first, then PM a mod to move).

**Proposed title:** `[Support] Will-Luck - iplayer-arr`

---

## Markdown version

# iplayer-arr

Official support thread for **iplayer-arr**, a BBC iPlayer download manager that plugs into Sonarr as both a Newznab indexer and a SABnzbd-compatible download client. It calls the BBC iBL API directly and remuxes HLS streams to MP4 with ffmpeg. No separate Newznab indexer required, no Perl/get_iplayer dependency.

## What it does

- **Newznab indexer** for Sonarr: BBC iPlayer shows up as a normal indexer Sonarr can search
- **SABnzbd-compatible download client**: Sonarr sends grabs to iplayer-arr, which downloads and remuxes the stream to MP4
- **Automatic episode numbering**: maps BBC iBL series / episodes to TVDB so Sonarr imports cleanly, including long-runners with no series prefix (Casualty, One Piece 1999) and year-suffixed brands (Doctor Who (2005))
- **Optional built-in WireGuard VPN** (PIA or generic WireGuard config) for use outside the UK
- **Web UI** with a dashboard, download history, manual search, and an Episode Overrides page for shows with tricky numbering

## Install

Template: `https://raw.githubusercontent.com/Will-Luck/unraid-templates/main/iplayer-arr.xml`

The template is pending review for the Community Applications feed. Until it lands there, you can install manually:

1. In Unraid, go to **Docker → Add Container**
2. Paste the template URL above into the **Template** field at the top and press **Tab**
3. Set paths, port, and (optionally) VPN env vars
4. **Apply**

Default web UI port: **62001** (changed from 8191 in v1.1.1 to avoid clashing with FlareSolverr). Appdata: **/mnt/user/appdata/iplayer-arr**. Downloads: **/mnt/user/downloads/iplayer** (point Sonarr's import path at the same directory).

## UK IP requirement

BBC iPlayer is geo-restricted to the UK. If you are outside the UK, you need one of:

- Unraid already running behind a UK VPN or proxy, OR
- The built-in WireGuard VPN enabled in the template (set `VPN_ENABLED=true` plus `VPN_PROVIDER` and credentials in the advanced section)

The template already includes the Docker flags the built-in VPN needs (`--cap-add=NET_ADMIN` and the `src_valid_mark` sysctl), so you don't need to add anything manually. Full VPN setup guide: https://github.com/Will-Luck/iplayer-arr/wiki/VPN-Configuration

## UK TV Licence required

You must hold a valid UK TV Licence to legally access BBC iPlayer content via iplayer-arr. iplayer-arr does not verify this and assumes you are compliant. Full legal terms: https://github.com/Will-Luck/iplayer-arr/blob/main/DISCLAIMER.md

## Current version

**v1.1.7** (released 2026-04-17):

- Position-based episode identity now survives Sonarr's tvsearch filter, fixing BBC long-runners with no "Series N" subtitle prefix (Casualty, One Piece 1999)
- Registry pages (GHCR and Docker Hub) now carry README and OCI metadata, so Docker Hub's description stays in sync with the repo on every release

Recent before that:

- **v1.1.6** (2026-04-16): Sonarr follow-up episode searches now carry `tvdbid` attribute, and store reverse lookup matches year-suffixed titles like "Doctor Who (2005)" against a bare "Doctor Who" query
- **v1.1.5** (2026-04-14): TVDB ID echoed in RSS responses for definitive series matching, parser fixes for subtitles with colons and `Series N: M.` prefixes, dashboard UX polish

Full changelog: https://github.com/Will-Luck/iplayer-arr/blob/main/CHANGELOG.md

## Links

- Project: https://github.com/Will-Luck/iplayer-arr
- Wiki (installation, configuration, Sonarr integration, troubleshooting): https://github.com/Will-Luck/iplayer-arr/wiki
- Docker image: `ghcr.io/will-luck/iplayer-arr:latest`
- Bug reports and feature requests: https://github.com/Will-Luck/iplayer-arr/issues

## Getting help

For install trouble, configuration questions, or VPN setup help, reply in this thread. For confirmed bugs or feature requests, please open a GitHub issue so it can be tracked and referenced in releases.

---

## Forum-ready BBCode version

If the rendered-view paste does not preserve formatting in the Unraid editor, copy everything below this line and paste as-is. Invision parses BBCode inline.

```bbcode
[size=5][b]iplayer-arr[/b][/size]

Official support thread for [b]iplayer-arr[/b], a BBC iPlayer download manager that plugs into Sonarr as both a Newznab indexer and a SABnzbd-compatible download client. It calls the BBC iBL API directly and remuxes HLS streams to MP4 with ffmpeg. No separate Newznab indexer required, no Perl/get_iplayer dependency.

[size=5][b]What it does[/b][/size]

[list]
[*][b]Newznab indexer[/b] for Sonarr: BBC iPlayer shows up as a normal indexer Sonarr can search
[*][b]SABnzbd-compatible download client[/b]: Sonarr sends grabs to iplayer-arr, which downloads and remuxes the stream to MP4
[*][b]Automatic episode numbering[/b]: maps BBC iBL series / episodes to TVDB so Sonarr imports cleanly, including long-runners with no series prefix (Casualty, One Piece 1999) and year-suffixed brands (Doctor Who (2005))
[*][b]Optional built-in WireGuard VPN[/b] (PIA or generic WireGuard config) for use outside the UK
[*][b]Web UI[/b] with a dashboard, download history, manual search, and an Episode Overrides page for shows with tricky numbering
[/list]

[size=5][b]Install[/b][/size]

Template: [url]https://raw.githubusercontent.com/Will-Luck/unraid-templates/main/iplayer-arr.xml[/url]

The template is pending review for the Community Applications feed. Until it lands there, you can install manually:

[list=1]
[*]In Unraid, go to [b]Docker -> Add Container[/b]
[*]Paste the template URL above into the [b]Template[/b] field at the top and press [b]Tab[/b]
[*]Set paths, port, and (optionally) VPN env vars
[*][b]Apply[/b]
[/list]

Default web UI port: [b]62001[/b] (changed from 8191 in v1.1.1 to avoid clashing with FlareSolverr). Appdata: [b]/mnt/user/appdata/iplayer-arr[/b]. Downloads: [b]/mnt/user/downloads/iplayer[/b] (point Sonarr's import path at the same directory).

[size=5][b]UK IP requirement[/b][/size]

BBC iPlayer is geo-restricted to the UK. If you are outside the UK, you need one of:

[list]
[*]Unraid already running behind a UK VPN or proxy, OR
[*]The built-in WireGuard VPN enabled in the template (set [b]VPN_ENABLED=true[/b] plus [b]VPN_PROVIDER[/b] and credentials in the advanced section)
[/list]

The template already includes the Docker flags the built-in VPN needs ([b]--cap-add=NET_ADMIN[/b] and the [b]src_valid_mark[/b] sysctl), so you don't need to add anything manually. Full VPN setup guide: [url]https://github.com/Will-Luck/iplayer-arr/wiki/VPN-Configuration[/url]

[size=5][b]UK TV Licence required[/b][/size]

You must hold a valid UK TV Licence to legally access BBC iPlayer content via iplayer-arr. iplayer-arr does not verify this and assumes you are compliant. Full legal terms: [url]https://github.com/Will-Luck/iplayer-arr/blob/main/DISCLAIMER.md[/url]

[size=5][b]Current version[/b][/size]

[b]v1.1.7[/b] (released 2026-04-17):

[list]
[*]Position-based episode identity now survives Sonarr's tvsearch filter, fixing BBC long-runners with no "Series N" subtitle prefix (Casualty, One Piece 1999)
[*]Registry pages (GHCR and Docker Hub) now carry README and OCI metadata, so Docker Hub's description stays in sync with the repo on every release
[/list]

Recent before that:

[list]
[*][b]v1.1.6[/b] (2026-04-16): Sonarr follow-up episode searches now carry tvdbid attribute, and store reverse lookup matches year-suffixed titles like "Doctor Who (2005)" against a bare "Doctor Who" query
[*][b]v1.1.5[/b] (2026-04-14): TVDB ID echoed in RSS responses for definitive series matching, parser fixes for subtitles with colons and "Series N: M." prefixes, dashboard UX polish
[/list]

Full changelog: [url]https://github.com/Will-Luck/iplayer-arr/blob/main/CHANGELOG.md[/url]

[size=5][b]Links[/b][/size]

[list]
[*]Project: [url]https://github.com/Will-Luck/iplayer-arr[/url]
[*]Wiki (installation, configuration, Sonarr integration, troubleshooting): [url]https://github.com/Will-Luck/iplayer-arr/wiki[/url]
[*]Docker image: [font=monospace]ghcr.io/will-luck/iplayer-arr:latest[/font]
[*]Bug reports and feature requests: [url]https://github.com/Will-Luck/iplayer-arr/issues[/url]
[/list]

[size=5][b]Getting help[/b][/size]

For install trouble, configuration questions, or VPN setup help, reply in this thread. For confirmed bugs or feature requests, please open a GitHub issue so it can be tracked and referenced in releases.
```
