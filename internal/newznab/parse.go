package newznab

import (
	"regexp"
	"strconv"
	"strings"
)

// EpisodeIdentity is the episode identity recovered from a release
// title. Every field is optional. A title that carries no recognisable
// numbering yields the zero value, which is a normal outcome and never
// an error: iPlayer-arr emits tier-4 "manual" titles and movie titles
// that genuinely have no season, episode or air date.
type EpisodeIdentity struct {
	// ShowName is the release-title prefix ahead of the numbering,
	// with dots restored to spaces. Empty when nothing was recognised.
	ShowName string
	// Season and Episode come from an SxxExx marker. Season 0 is the
	// specials season that buildSxxExxTitle emits as S00E<mmdd>.
	Season  int
	Episode int
	// AirDate is an ISO "YYYY-MM-DD" date, set for the date-tier
	// releases used by BBC daily soaps and sports fixtures, where the
	// date IS the episode identity.
	AirDate string
}

var (
	// reTitleSxxExx matches the SxxExx marker emitted by
	// buildSxxExxTitle. The episode is allowed up to four digits
	// because specials use S00E<mmdd> (e.g. S00E1225). The leading and
	// trailing groups act as manual word boundaries, which RE2 has no
	// lookaround for: they stop a show name such as "Se7en" or a
	// resolution such as "1080p" from being read as numbering.
	reTitleSxxExx = regexp.MustCompile(`(^|[^A-Za-z0-9])[Ss](\d{1,2})[Ee](\d{1,4})([^0-9]|$)`)

	// reTitleAirDate matches the dotted YYYY.MM.DD date emitted by
	// buildDateTitle. The two-digit month and day are exact-width so a
	// movie title such as "Blade.Runner.2049.2017.1080p" (year followed
	// by year followed by resolution) cannot be misread as a date.
	reTitleAirDate = regexp.MustCompile(`(^|[^0-9])(\d{4})[.\-](\d{2})[.\-](\d{2})([^0-9]|$)`)
)

// ParseTitle recovers the episode identity from a release title. It is
// the read side of GenerateTitle and understands exactly the formats
// this project emits.
//
// It is total: any input, including an empty string or a bare PID,
// returns a value rather than an error, and unrecognised input returns
// the zero EpisodeIdentity. Callers persist the result as-is; the
// download queue's ordering treats zero values as "no identity" and
// falls back to creation time.
func ParseTitle(title string) EpisodeIdentity {
	t := strings.TrimSpace(title)
	// A client may hand back the NZB filename rather than the release
	// name. handleAdd already strips this, but ParseTitle is called
	// from Enqueue, which several paths reach directly.
	t = strings.TrimSuffix(t, ".nzb")
	if t == "" {
		return EpisodeIdentity{}
	}

	// SxxExx wins over a date: a release that carries real numbering is
	// matched by Sonarr on that numbering, so that is its identity even
	// if the episode subtitle happens to contain a date too.
	if m := reTitleSxxExx.FindStringSubmatchIndex(t); m != nil {
		season, sErr := strconv.Atoi(t[m[4]:m[5]])
		episode, eErr := strconv.Atoi(t[m[6]:m[7]])
		if sErr == nil && eErr == nil {
			return EpisodeIdentity{
				ShowName: showNameFromPrefix(t[:m[3]]),
				Season:   season,
				Episode:  episode,
			}
		}
	}

	if m := reTitleAirDate.FindStringSubmatchIndex(t); m != nil {
		year, yErr := strconv.Atoi(t[m[4]:m[5]])
		month, mErr := strconv.Atoi(t[m[6]:m[7]])
		day, dErr := strconv.Atoi(t[m[8]:m[9]])
		if yErr == nil && mErr == nil && dErr == nil &&
			year >= 1900 && year <= 2199 &&
			month >= 1 && month <= 12 &&
			day >= 1 && day <= 31 {
			return EpisodeIdentity{
				ShowName: showNameFromPrefix(t[:m[3]]),
				AirDate:  t[m[4]:m[5]] + "-" + t[m[6]:m[7]] + "-" + t[m[8]:m[9]],
			}
		}
	}

	return EpisodeIdentity{}
}

// showNameFromPrefix turns the dot-separated release-title prefix that
// precedes the numbering back into a readable show name.
func showNameFromPrefix(prefix string) string {
	name := strings.ReplaceAll(prefix, ".", " ")
	name = strings.Join(strings.Fields(name), " ")
	return strings.TrimSpace(name)
}
