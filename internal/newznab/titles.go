package newznab

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

const releaseGroup = "iParr"

var (
	reSpecialMarker = regexp.MustCompile(`(?i)\b(special|christmas|easter|new year|halloween|bonfire)\b`)
	reUnsafe        = regexp.MustCompile(`[^a-zA-Z0-9.\- ]`)
	reMultiDot      = regexp.MustCompile(`\.{2,}`)
	// reDateSubtitle matches a bare date subtitle in DD/MM/YYYY (or .  / -)
	// form, used by BBC daily soaps where the iPlayer subtitle is just the
	// air date and there is no real series/episode numbering.
	reDateSubtitle = regexp.MustCompile(`^\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$`)

	// reCompositeDateSubtitle matches subtitles where a non-colon prefix is
	// followed by a date, e.g. "2025/26: 22/03/2026" (BBC Match of the Day
	// format). Anchored so it does not false-positive on episode titles
	// that merely contain a date substring.
	reCompositeDateSubtitle = regexp.MustCompile(`^[^:]+:\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$`)
)

// isDateSubtitle reports whether s looks like a bare date (the only thing in
// the subtitle field), as opposed to a normal episode title.
func isDateSubtitle(s string) bool {
	return reDateSubtitle.MatchString(s)
}

// isDateLikeSubtitle reports whether a subtitle is either a bare date
// (DD/MM/YYYY) or a composite prefix-and-date format. Returns true for
// cases where the subtitle carries no episode information worth
// preserving in the output filename.
func isDateLikeSubtitle(s string) bool {
	return reDateSubtitle.MatchString(s) || reCompositeDateSubtitle.MatchString(s)
}

// GenerateTitle builds a Sonarr-compatible release title for the given
// programme using the 4-tier identity resolution chain.  It returns the
// formatted title and the tier constant (store.TierFull, store.TierPosition,
// store.TierDate, or store.TierManual) that was used.
func GenerateTitle(p *store.Programme, quality string, override *store.ShowOverride) (string, string) {
	name := p.Name
	episode := p.Episode
	series := p.Series
	episodeNum := p.EpisodeNum
	position := p.Position
	airDate := p.AirDate

	if override != nil {
		if override.CustomName != "" {
			name = override.CustomName
		}
		if override.ForceDateBased && airDate != "" {
			return buildDateTitle(name, episode, airDate, quality), store.TierDate
		}
		if override.ForcePosition {
			series = 0
			episodeNum = 0
		}
		if override.ForceSeriesNum > 0 {
			series = override.ForceSeriesNum
		}
		series += override.SeriesOffset
		episodeNum += override.EpisodeOffset
	}

	// Specials: episode title matches a known special keyword and no regular
	// series/episode numbering is available.  Use S00E<mmdd> from the air date.
	if isSpecial(episode) && (series == 0 || episodeNum == 0) {
		epNum := position
		if epNum == 0 && airDate != "" {
			epNum = mmddFromDate(airDate)
		}
		if epNum > 0 {
			return buildSxxExxTitle(name, episode, 0, epNum, quality), store.TierFull
		}
	}

	// Tier 1: both series and episode number are known.
	if series > 0 && episodeNum > 0 {
		return buildSxxExxTitle(name, episode, series, episodeNum, quality), store.TierFull
	}

	// Tier 1.5: BBC daily soaps (EastEnders, Casualty, Holby City, Doctors,
	// Coronation Street, Neighbours, ...) come from iPlayer with the
	// subtitle as a literal date and parent_position as a flat cumulative
	// counter. Without this branch we would fall through to Tier 2 and emit
	// "S01E<position>" — Sonarr's parser would then try to look up
	// season 1 episode <position> in TVDB and reject the release because no
	// such episode exists. Promote to date tier so Sonarr matches by air
	// date instead. The redundant date subtitle is dropped from the title
	// to avoid the "S01E7307.06042026" tail.
	if airDate != "" && isDateSubtitle(episode) {
		return buildDateTitle(name, "", airDate, quality), store.TierDate
	}

	// Tier 2: use parent_position as the episode number within series 1.
	if position > 0 {
		s := 1
		if override != nil && override.ForceSeriesNum > 0 {
			s = override.ForceSeriesNum
		}
		return buildSxxExxTitle(name, episode, s, position, quality), store.TierPosition
	}

	// Tier 3: fall back to air date.
	if airDate != "" {
		return buildDateTitle(name, episode, airDate, quality), store.TierDate
	}

	// Tier 4: no numbering available; title only.
	return buildManualTitle(name, episode, quality), store.TierManual
}

func buildSxxExxTitle(name, episode string, series, ep int, quality string) string {
	sn := sanitiseForTitle(name)
	se := sanitiseForTitle(episode)
	seNum := fmt.Sprintf("S%02dE%02d", series, ep)
	if se != "" {
		return fmt.Sprintf("%s.%s.%s.%s.WEB-DL.AAC.H264-%s", sn, seNum, se, quality, releaseGroup)
	}
	return fmt.Sprintf("%s.%s.%s.WEB-DL.AAC.H264-%s", sn, seNum, quality, releaseGroup)
}

func buildDateTitle(name, episode, airDate, quality string) string {
	if isDateLikeSubtitle(episode) {
		episode = ""
	}
	sn := sanitiseForTitle(name)
	se := sanitiseForTitle(episode)
	date := strings.ReplaceAll(airDate, "-", ".")
	if se != "" {
		return fmt.Sprintf("%s.%s.%s.%s.WEB-DL.AAC.H264-%s", sn, date, se, quality, releaseGroup)
	}
	return fmt.Sprintf("%s.%s.%s.WEB-DL.AAC.H264-%s", sn, date, quality, releaseGroup)
}

func buildManualTitle(name, episode, quality string) string {
	sn := sanitiseForTitle(name)
	se := sanitiseForTitle(episode)
	if se != "" {
		return fmt.Sprintf("%s.%s.%s.WEB-DL.AAC.H264-%s", sn, se, quality, releaseGroup)
	}
	return fmt.Sprintf("%s.%s.WEB-DL.AAC.H264-%s", sn, quality, releaseGroup)
}

// sanitiseForTitle converts a human-readable string into a dot-separated,
// filesystem-safe title fragment suitable for use in a release name.
func sanitiseForTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "&", "and")
	s = strings.ReplaceAll(s, "'", "")
	s = reUnsafe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", ".")
	s = reMultiDot.ReplaceAllString(s, ".")
	s = strings.Trim(s, ".")
	return s
}

func isSpecial(episode string) bool {
	return reSpecialMarker.MatchString(episode)
}

// mmddFromDate extracts a compact MMDD integer from a "YYYY-MM-DD" date
// string, used for the episode number of specials (e.g. S00E1225).
func mmddFromDate(airDate string) int {
	parts := strings.Split(airDate, "-")
	if len(parts) != 3 {
		return 0
	}
	var mm, dd int
	fmt.Sscanf(parts[1], "%d", &mm)
	fmt.Sscanf(parts[2], "%d", &dd)
	return mm*100 + dd
}
