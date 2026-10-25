package newznab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// GitHub #60. BBC data has no TheTVDB numbers for specials. iPlayer lists
// most of them directly under the brand, so parent_position counts the
// series as well ("Porridge" Christmas specials at 4 and 5), and
// GenerateTitle turned that into S01E<position> or S00E<position>, which
// Sonarr imported as another episode. Some regular episodes are numbered
// differently too (Doctor Who Series 7 counts "The Snowmen" as episode 6,
// QI's series are letters). When a search carries a tvdbid, every
// episode whose title and air date match one of that series' TheTVDB
// episodes takes TheTVDB's numbers, which are the ones Sonarr uses.

// TVDBEpisode is one episode of a TheTVDB series, in the aired order
// Sonarr uses.
type TVDBEpisode struct {
	Season  int
	Episode int
	Title   string
	AirDate string // YYYY-MM-DD; "" when TheTVDB has none
}

// EpisodeSource lists the TheTVDB episodes of a series.
type EpisodeSource interface {
	Episodes(ctx context.Context, tvdbid string) ([]TVDBEpisode, error)
}

const (
	skyhookEpisodesTTL     = 12 * time.Hour
	skyhookEpisodesFailTTL = 5 * time.Minute
	skyhookEpisodesTimeout = 15 * time.Second
	// Doctor Who (2005) is about 170 KB; a long-running daily soap with
	// thousands of episodes stays well under this.
	skyhookEpisodesMaxBody = 16 << 20
)

// SkyhookEpisodes reads episode lists from Skyhook, the TheTVDB proxy
// Sonarr itself uses, so the numbers are the ones Sonarr has. A list is
// cached for skyhookEpisodesTTL, and a failure for
// skyhookEpisodesFailTTL so a Skyhook outage costs one request per
// series every few minutes rather than one per search.
type SkyhookEpisodes struct {
	client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]skyhookEpisodesEntry
}

type skyhookEpisodesEntry struct {
	episodes []TVDBEpisode
	err      error
	expires  time.Time
}

func NewSkyhookEpisodes() *SkyhookEpisodes {
	return &SkyhookEpisodes{
		client: &http.Client{Timeout: skyhookEpisodesTimeout},
		now:    time.Now,
		cache:  make(map[string]skyhookEpisodesEntry),
	}
}

func (s *SkyhookEpisodes) Episodes(ctx context.Context, tvdbid string) ([]TVDBEpisode, error) {
	if tvdbid == "" || strings.Trim(tvdbid, "0123456789") != "" {
		return nil, fmt.Errorf("tvdbid %q is not a number", tvdbid)
	}
	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[tvdbid]; ok && now.Before(e.expires) {
		s.mu.Unlock()
		return e.episodes, e.err
	}
	s.mu.Unlock()

	eps, err := fetchSkyhookEpisodes(ctx, s.client, tvdbid)
	if err != nil && ctx.Err() != nil {
		// The search was cancelled, which says nothing about Skyhook.
		return nil, err
	}
	// An empty list is more likely Skyhook mid-refresh than a series
	// with no episodes, so it is retried as soon as a failure would be.
	ttl := skyhookEpisodesTTL
	if err != nil || len(eps) == 0 {
		ttl = skyhookEpisodesFailTTL
	}
	s.mu.Lock()
	for id, e := range s.cache {
		if !now.Before(e.expires) {
			delete(s.cache, id)
		}
	}
	s.cache[tvdbid] = skyhookEpisodesEntry{episodes: eps, err: err, expires: now.Add(ttl)}
	s.mu.Unlock()
	return eps, err
}

func fetchSkyhookEpisodes(ctx context.Context, client *http.Client, tvdbid string) ([]TVDBEpisode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skyhookBaseURL+"/v1/tvdb/shows/en/"+tvdbid, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skyhook returned status %d", resp.StatusCode)
	}
	var show struct {
		Episodes []struct {
			SeasonNumber  int    `json:"seasonNumber"`
			EpisodeNumber int    `json:"episodeNumber"`
			Title         string `json:"title"`
			AirDate       string `json:"airDate"`
		} `json:"episodes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, skyhookEpisodesMaxBody)).Decode(&show); err != nil {
		return nil, fmt.Errorf("decode skyhook episodes: %w", err)
	}
	eps := make([]TVDBEpisode, 0, len(show.Episodes))
	for _, e := range show.Episodes {
		eps = append(eps, TVDBEpisode{
			Season:  e.SeasonNumber,
			Episode: e.EpisodeNumber,
			Title:   e.Title,
			AirDate: e.AirDate,
		})
	}
	return eps, nil
}

// tvdbMatchWindow is how far apart the iPlayer and TheTVDB air dates of
// a title match may be. Box sets released on iPlayer in one go sit up to
// a week before TheTVDB's broadcast date for the next episode. A wider
// window starts matching a generic title such as "Episode 1" or "Week 4
// Results" to the same title in another series.
const tvdbMatchWindow = 7 * 24 * time.Hour

// tvdbOneOffWindow is tvdbMatchWindow for a one-off (oneOffTitle), whose
// title is the whole programme's rather than one of a series' episode
// titles. iPlayer dates "Doctor Who: Dreamland" 2009-12-05, its BBC Two
// showing, and TheTVDB 2009-12-24. In the 55 series surveyed for Gitea
// #104, two TheTVDB episodes of one series with the same specific title
// aired either within a week of each other or at least 105 days apart,
// and the same title years away can be another programme: TheTVDB's
// "The Ultimate Guide" (Doctor Who (2005) S00E18, 2010) is not iPlayer's
// 2013 one, which TheTVDB calls "The Ultimate Guide (2013)" (S00E91).
const tvdbOneOffWindow = 31 * 24 * time.Hour

// tvdbIndex is a series' dated TheTVDB episodes by normalised title, by
// number and, for specials, by air date, and the first and last of
// their dates.
type tvdbIndex struct {
	byTitle     map[string][]TVDBEpisode
	byNumber    map[[2]int]TVDBEpisode
	specialsOn  map[string][]TVDBEpisode
	first, last time.Time
}

func newTVDBIndex(eps []TVDBEpisode) tvdbIndex {
	idx := tvdbIndex{
		byTitle:    map[string][]TVDBEpisode{},
		byNumber:   map[[2]int]TVDBEpisode{},
		specialsOn: map[string][]TVDBEpisode{},
	}
	for _, e := range eps {
		if e.Episode < 1 {
			continue
		}
		aired, err := time.Parse("2006-01-02", e.AirDate)
		if err != nil {
			continue
		}
		if k := episodeTitleKey(e.Title); k != "" {
			idx.byTitle[k] = append(idx.byTitle[k], e)
		}
		idx.byNumber[[2]int{e.Season, e.Episode}] = e
		if idx.first.IsZero() || aired.Before(idx.first) {
			idx.first = aired
		}
		if aired.After(idx.last) {
			idx.last = aired
		}
		if e.Season == 0 {
			day := aired.Format("2006-01-02")
			idx.specialsOn[day] = append(idx.specialsOn[day], e)
		}
	}
	return idx
}

// How a programme matched a TheTVDB episode, weakest first. A title
// matched outside tvdbMatchWindow has nothing else to back it, a match
// on what follows a grouping label could be an extra about the episode,
// and one on the air date alone could be anything else shown that day.
const (
	matchedOffWindow = iota
	matchedOnAirDate
	matchedOnLabel
	matchedOnTitle
)

// tvdbMatch is a TheTVDB episode matched to an iPlayer programme.
type tvdbMatch struct {
	TVDBEpisode
	strength int
}

// withinMatchWindow reports whether two YYYY-MM-DD dates are no more
// than tvdbMatchWindow apart. An unparseable date is never within it.
func withinMatchWindow(a, b string) bool {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return false
	}
	d := ta.Sub(tb)
	return d >= -tvdbMatchWindow && d <= tvdbMatchWindow
}

// match returns the one TheTVDB episode whose title matches a candidate
// title of the iPlayer subtitle and whose air date is within
// tvdbMatchWindow of airDate. No match, or more than one, returns false.
func (idx tvdbIndex) match(subtitle, airDate string) (tvdbMatch, bool) {
	m, n := idx.titleMatches(subtitle, airDate)
	return m, n == 1
}

// titleMatches is match, returning how many TheTVDB episodes matched.
func (idx tvdbIndex) titleMatches(subtitle, airDate string) (tvdbMatch, int) {
	if _, err := time.Parse("2006-01-02", airDate); err != nil {
		return tvdbMatch{}, 0
	}
	var found tvdbMatch
	n := 0
	for _, c := range episodeTitleCandidates(subtitle) {
		strength := matchedOnTitle
		if c.weak {
			strength = matchedOnLabel
		}
		for _, e := range idx.byTitle[c.key] {
			if !withinMatchWindow(e.AirDate, airDate) {
				continue
			}
			if n > 0 && e.Season == found.Season && e.Episode == found.Episode {
				found.strength = max(found.strength, strength)
				continue
			}
			found = tvdbMatch{TVDBEpisode: e, strength: strength}
			n++
		}
	}
	return found, n
}

// specialOn returns the one TheTVDB special that aired on airDate. When
// several did, it is the one whose title, if specific, the subtitle
// contains ("Mini Episode - The Night of the Doctor" holds "The Night of
// the Doctor"), if only one is.
func (idx tvdbIndex) specialOn(subtitle, airDate string) (tvdbMatch, bool) {
	aired, err := time.Parse("2006-01-02", airDate)
	if err != nil {
		return tvdbMatch{}, false
	}
	eps := idx.specialsOn[aired.Format("2006-01-02")]
	if len(eps) > 1 {
		key := " " + episodeTitleKey(subtitle) + " "
		var named []TVDBEpisode
		for _, e := range eps {
			if k := episodeTitleKey(e.Title); specificTitleKey(k) && strings.Contains(key, " "+k+" ") {
				named = append(named, e)
			}
		}
		eps = named
	}
	if len(eps) != 1 {
		return tvdbMatch{}, false
	}
	return tvdbMatch{TVDBEpisode: eps[0], strength: matchedOnAirDate}, true
}

// What TheTVDB says about the number positionGuess gave a programme.
const (
	guessUnchecked    = iota // no dated TheTVDB episode at that number
	guessConfirmed           // the episode there has its title, or aired that day
	guessNear                // the episode there aired within tvdbMatchWindow
	guessContradicted        // the episode there aired further away
)

func (idx tvdbIndex) checkGuess(season, episode int, subtitle, airDate string) int {
	e, ok := idx.byNumber[[2]int{season, episode}]
	if !ok {
		return guessUnchecked
	}
	if k := episodeTitleKey(subtitle); k != "" && k == episodeTitleKey(e.Title) {
		return guessConfirmed
	}
	if _, err := time.Parse("2006-01-02", airDate); err != nil {
		return guessUnchecked
	}
	switch {
	case e.AirDate == airDate:
		return guessConfirmed
	case withinMatchWindow(e.AirDate, airDate):
		return guessNear
	}
	return guessContradicted
}

// givenNumber returns the number GenerateTitle gives p, with no override,
// when TheTVDB does not number it: its guess (positionGuess), or the
// series and episode from BBC data.
func givenNumber(p *store.Programme) (season, episode int, ok bool) {
	if s, e, guessed := positionGuess(p); guessed {
		return s, e, true
	}
	if p.Series > 0 && p.EpisodeNum > 0 {
		return p.Series, p.EpisodeNum, true
	}
	return 0, 0, false
}

// assumedNumber reports whether part of the number BBC data gives p is
// iplayer-arr's assumption: Series 1 for an episode number with no series
// (GitHub #32), an episode number from air-date order, or a story's part
// number read as the season's episode number. Each was found numbering
// another episode: QI's "Series F: 1. Family" as S01E01 (TheTVDB S06E01),
// a Strictly Come Dancing clip as S24E05, classic Doctor Who's "Season 4:
// The Tenth Planet: Episode 1" as S04E01 (TheTVDB S04E05). Gitea #104.
func assumedNumber(p *store.Programme) bool {
	return p.SeriesAssumed || p.EpisodeAssumed || storyPart(p.Episode)
}

// storyPart reports whether subtitle numbers a part of a story under a
// series label ("Season 4: The Tenth Planet: Episode 1"), a number BBC
// data takes as the episode's.
func storyPart(subtitle string) bool {
	_, rest, ok := strings.Cut(subtitle, ": ")
	if !ok {
		return false
	}
	story, part, ok := strings.Cut(rest, ": ")
	return ok && !reLeadingEpisodeNum.MatchString(story) && reEpisodePart.MatchString(part)
}

// A series' run on TheTVDB, from its first dated episode to its last,
// specials included. A programme cannot be shown long before its series
// starts. One shown long after it ends, when TheTVDB bears out nothing
// iPlayer has of the show, is another show of the name: iPlayer's bare
// "Gladiators" brand is the 2024 series, aired 24 years after the last
// episode of TheTVDB 81208, the 1992 one. Ten years leaves room for a
// repeat: iPlayer dates Keeping Up Appearances' "Series 5: Episode 1"
// 2002, seven years after TheTVDB's regular episodes. Gitea #104.
const (
	tvdbBeforeRun = 365 * 24 * time.Hour
	tvdbAfterRun  = 10 * 365 * 24 * time.Hour
)

// beyondRun reports whether airDate is more than tvdbBeforeRun before the
// series' run on TheTVDB or more than tvdbAfterRun after it. An
// unparseable date is not.
func (idx tvdbIndex) beyondRun(airDate string) bool {
	aired, err := time.Parse("2006-01-02", airDate)
	if err != nil || idx.first.IsZero() {
		return false
	}
	return aired.Before(idx.first.Add(-tvdbBeforeRun)) || aired.After(idx.last.Add(tvdbAfterRun))
}

// titleAnyDate returns the one TheTVDB episode, aired on any date, that a
// specific title of p's subtitle matches, when TheTVDB bears out no
// number p goes out with (checkGuess). A title match needs the air dates
// within tvdbMatchWindow because a generic title such as "Episode 1"
// matches another series' episode; a specific one TheTVDB has only once
// in the series is that episode, whenever iPlayer dates it: Red Dwarf's
// "X: 1. Trojan", dated only "2012", is TheTVDB's S10E01, and QI's
// "Series P: 2. Peril", shown a month after TheTVDB's date, is S16E02.
// It is not taken when p aired more than tvdbBeforeRun before that
// episode, which then belongs to another show of the name. Gitea #104.
func (idx tvdbIndex) titleAnyDate(p *store.Programme) (tvdbMatch, bool) {
	s, e, ok := givenNumber(p)
	if !ok {
		return tvdbMatch{}, false
	}
	if v := idx.checkGuess(s, e, p.Episode, p.AirDate); v == guessConfirmed || v == guessNear {
		return tvdbMatch{}, false
	}
	var found TVDBEpisode
	n := 0
	for _, c := range episodeTitleCandidates(p.Episode) {
		if !specificTitleKey(c.key) {
			continue
		}
		for _, ep := range idx.byTitle[c.key] {
			if n > 0 && ep.Season == found.Season && ep.Episode == found.Episode {
				continue
			}
			found = ep
			n++
		}
	}
	if n != 1 {
		return tvdbMatch{}, false
	}
	if aired, err := time.Parse("2006-01-02", p.AirDate); err == nil {
		if first, _ := time.Parse("2006-01-02", found.AirDate); aired.Before(first.Add(-tvdbBeforeRun)) {
			return tvdbMatch{}, false
		}
	}
	return tvdbMatch{TVDBEpisode: found, strength: matchedOffWindow}, true
}

// contradicts reports whether TheTVDB has an episode with another title,
// aired more than tvdbMatchWindow from p, at the number p goes out with,
// when that number may not be p's: a special's guess (GitHub #60), a
// number partly assumed (assumedNumber), or BBC's number for a programme
// aired well outside the series' run (beyondRun) when TheTVDB bears out
// nothing of the show (Gitea #104). Other guesses are not doubted, as in
// 1.9.0: a bought-in series reaches iPlayer years after TheTVDB's dates.
func (idx tvdbIndex) contradicts(p *store.Programme, showMatched bool) bool {
	s, e, ok := givenNumber(p)
	if !ok {
		return false
	}
	_, _, guessed := positionGuess(p)
	doubtful := guessed && isSpecial(p.Episode) ||
		!guessed && (assumedNumber(p) || !showMatched && idx.beyondRun(p.AirDate))
	return doubtful && idx.checkGuess(s, e, p.Episode, p.AirDate) == guessContradicted
}

// tvdbClaim is a programme's claim on a TheTVDB episode's number.
type tvdbClaim struct {
	prog *store.Programme
	tvdbMatch
}

func (c tvdbClaim) apply() {
	c.prog.Series = c.Season
	c.prog.EpisodeNum = c.Episode
	c.prog.TVDBNumbered = true
}

// resolve matches each programme eligible accepts to the series' TheTVDB
// episodes, without changing any. A programme claims the episode its
// subtitle matches. Failing that, a programme datable accepts whose only
// number is a guess (positionGuess) claims the one special TheTVDB has
// on its air date: BBC titles its specials "Christmas Special 2022"
// where TheTVDB has "The Death Ghost". It does not when the guess is
// borne out, or is near and not a special's number; nor when its title
// fits more than one TheTVDB episode. Failing both, a programme datable
// accepts claims the episode its specific title matches at any date
// (titleAnyDate). When programmes with different PIDs claim one episode,
// another version of a programme ("<subtitle> - Signed", "<subtitle>
// (Animated)") gives way to the version it is of, a weaker claim gives
// way to a stronger one, and claims below a full title match keep the
// number only if their subtitles are the same (one programme listed
// twice). Daily soaps whose subtitle is the date keep their date titles.
//
// It returns the claims that keep their numbers, and the programmes left
// without one that contradicts reports. TheTVDB bears the show out when
// it numbers one of its programmes by a match within tvdbMatchWindow, or
// bears out the number one goes out with, by its title at any date or by
// its air date. When it bears out nothing, no programme keeps a number
// matched outside the window.
func (idx tvdbIndex) resolve(progs []*store.Programme, eligible, datable func(*store.Programme) bool) (won []tvdbClaim, contradicted []*store.Programme) {
	claims := map[[2]int][]tvdbClaim{}
	var considered []*store.Programme
	for _, p := range progs {
		if !eligible(p) || isDateLikeSubtitle(p.Episode) {
			continue
		}
		considered = append(considered, p)
		m, n := idx.titleMatches(p.Episode, p.AirDate)
		if n != 1 {
			if n > 1 || !datable(p) {
				continue
			}
			var ok bool
			if s, e, guessed := positionGuess(p); guessed {
				verdict := idx.checkGuess(s, e, p.Episode, p.AirDate)
				if verdict == guessConfirmed || verdict == guessNear && s != 0 {
					continue
				}
				m, ok = idx.specialOn(p.Episode, p.AirDate)
			}
			if !ok {
				if m, ok = idx.titleAnyDate(p); !ok {
					continue
				}
			}
		}
		k := [2]int{m.Season, m.Episode}
		claims[k] = append(claims[k], tvdbClaim{prog: p, tvdbMatch: m})
	}
	numbered := map[*store.Programme]bool{}
	for _, cs := range claims {
		claimed := map[string]bool{}
		for _, c := range cs {
			claimed[c.prog.Episode] = true
		}
		versionOf := func(c tvdbClaim) bool {
			base := reVersionSuffix.ReplaceAllString(c.prog.Episode, "")
			return base != c.prog.Episode && claimed[base]
		}
		var kept []tvdbClaim
		for _, c := range cs {
			if !versionOf(c) {
				kept = append(kept, c)
			}
		}
		cs = kept
		// When programmes claim one number only by titles matched
		// outside tvdbMatchWindow, the one that goes out with that number
		// anyway keeps it: the BBC titles Naruto's episodes 16 to 18 all
		// "The Broken Seal", TheTVDB's title for 16. Gitea #104.
		if len(cs) > 1 {
			offWindow := true
			var own []tvdbClaim
			ownPIDs := map[string]bool{}
			for _, c := range cs {
				offWindow = offWindow && c.strength == matchedOffWindow
				if s, e, ok := givenNumber(c.prog); ok && s == c.Season && e == c.Episode {
					own = append(own, c)
					ownPIDs[c.prog.PID] = true
				}
			}
			// One programme listed twice is one claimant.
			if offWindow && len(ownPIDs) == 1 {
				cs = own
			}
		}
		pids := map[string]bool{}
		subtitles := map[int]map[string]bool{}
		best := matchedOffWindow
		for _, c := range cs {
			pids[c.prog.PID] = true
			if subtitles[c.strength] == nil {
				subtitles[c.strength] = map[string]bool{}
			}
			subtitles[c.strength][c.prog.Episode] = true
			best = max(best, c.strength)
		}
		for _, c := range cs {
			if c.strength < matchedOnTitle && len(pids) > 1 && (c.strength < best || len(subtitles[c.strength]) > 1) {
				continue
			}
			won = append(won, c)
			numbered[c.prog] = true
		}
	}
	// atOwnNumber reports whether TheTVDB bears out the number p goes out
	// with: by its title at any date, or by its air date.
	wonAt := make(map[*store.Programme]tvdbClaim, len(won))
	for _, c := range won {
		wonAt[c.prog] = c
	}
	atOwnNumber := func(p *store.Programme) bool {
		s, e, ok := givenNumber(p)
		if !ok {
			return false
		}
		if c, won := wonAt[p]; won {
			return c.Season == s && c.Episode == e
		}
		v := idx.checkGuess(s, e, p.Episode, p.AirDate)
		return v == guessConfirmed || v == guessNear
	}
	// A title matched outside tvdbMatchWindow at another number does not
	// bear the show out: iPlayer's Teletubbies are the 2015 series, three
	// of whose episodes share a title with one of the 1997 series'.
	showMatched := false
	for _, c := range won {
		showMatched = showMatched || c.strength > matchedOffWindow
	}
	// labels holds, per label an assumed series is numbered under, the
	// PIDs of the programmes TheTVDB bears out at their own numbers, and
	// of those it has an episode at the own number of. Where it has none,
	// it can neither bear the number out nor contradict it: TheTVDB splits
	// Naruto's Part I into five seasons, so "Part I: 131." has no S01E131.
	// PIDs, as iBL can list a programme twice.
	labels := map[string]labelPIDs{}
	for _, p := range considered {
		own := atOwnNumber(p)
		showMatched = showMatched || own
		if l := seriesLabel(p.Episode); l != "" && p.SeriesAssumed {
			s, e, _ := givenNumber(p)
			if _, listed := idx.byNumber[[2]int{s, e}]; listed {
				lp := labels[l]
				if lp.checkable == nil {
					lp = labelPIDs{borne: map[string]bool{}, checkable: map[string]bool{}}
					labels[l] = lp
				}
				lp.checkable[p.PID] = true
				if own {
					lp.borne[p.PID] = true
				}
			}
		}
	}
	// With nothing of the show borne out, a title matched outside the
	// window at another number is another show's: the 2015 Teletubbies'
	// "Bubbles" is not the 1997 series' S01E40. Left unnumbered, such
	// programmes face contradicts like the rest.
	if !showMatched {
		for _, c := range won {
			delete(numbered, c.prog)
		}
		won = nil
	}
	for _, p := range considered {
		if !numbered[p] && !labelBorneOut(p, labels) && idx.contradicts(p, showMatched) {
			contradicted = append(contradicted, p)
		}
	}
	return won, contradicted
}

// seriesLabel returns the label a subtitle numbers an episode under,
// "Part I" for "Part I: 13. Crystal Ice Mirrors", or "".
func seriesLabel(subtitle string) string {
	label, rest, ok := strings.Cut(subtitle, ": ")
	if !ok || !reLeadingEpisodeNum.MatchString(rest) {
		return ""
	}
	return label
}

// labelBorneOut reports whether p's number, assumed Series 1 under a
// label (GitHub #32), stands because TheTVDB bears out at least three of
// the label's programmes at their own numbers, and half of those it has
// an episode at the own number of. Naruto
// reached iPlayer in 2026 as "Part I: N. Title", 23 years after
// TheTVDB's dates, and its titles differ from TheTVDB's by translation
// ("Crystal Ice Mirrors", "Demonic Mirroring Ice Crystals"). Its "Boruto:
// N." programmes, which TheTVDB bears out nowhere, are another series.
// Gitea #104.
func labelBorneOut(p *store.Programme, labels map[string]labelPIDs) bool {
	if !p.SeriesAssumed || p.EpisodeAssumed || storyPart(p.Episode) {
		return false
	}
	lp := labels[seriesLabel(p.Episode)]
	return len(lp.borne) >= 3 && 2*len(lp.borne) >= len(lp.checkable)
}

// labelPIDs is what labelBorneOut counts for one label.
type labelPIDs struct {
	borne, checkable map[string]bool
}

// numberFromTVDB gives the programmes eligible accepts TheTVDB's numbers
// as resolve decides, marks those whose number TheTVDB contradicts, and
// returns how many it numbered.
func (idx tvdbIndex) numberFromTVDB(progs []*store.Programme, eligible, datable func(*store.Programme) bool) int {
	won, contradicted := idx.resolve(progs, eligible, datable)
	for _, c := range won {
		c.apply()
	}
	for _, p := range contradicted {
		p.TVDBContradicted = true
	}
	return len(won)
}

// markUnchecked marks the programmes eligible accepts whose only number
// is a guess (positionGuess), for when the TheTVDB list that would check
// it is unavailable, and returns how many it marked. Regular episodes
// count too, not only the specials resolve leaves out when TheTVDB
// contradicts them: without the list no title match moves "The Runaway
// Bride" off its guessed S01E04, the number of "Aliens of London".
// Gitea #104.
func markUnchecked(progs []*store.Programme, eligible func(*store.Programme) bool) int {
	n := 0
	for _, p := range progs {
		if _, _, guessed := positionGuess(p); guessed && eligible(p) {
			p.TVDBUnchecked = true
			n++
		}
	}
	return n
}

// matchOneOff returns the one TheTVDB episode whose title is a one-off's
// own title, or its whole iPlayer title (TheTVDB lists "Gavin & Stacey:
// A Fond Farewell" so), and that aired within tvdbOneOffWindow of
// airDate. A title of generic words alone never matches, nor does an air
// date alone: TheTVDB has "The Next Doctor" on the day iPlayer dates
// "Doctor Who: Top 5 Christmas Moments".
func (idx tvdbIndex) matchOneOff(title, fullTitle, airDate string) (TVDBEpisode, bool) {
	aired, err := time.Parse("2006-01-02", airDate)
	if err != nil || !specificTitleKey(episodeTitleKey(title)) {
		return TVDBEpisode{}, false
	}
	var found []TVDBEpisode
	for _, k := range []string{episodeTitleKey(title), episodeTitleKey(fullTitle)} {
		for _, e := range idx.byTitle[k] {
			// newTVDBIndex lists only episodes with a valid air date.
			t, _ := time.Parse("2006-01-02", e.AirDate)
			if d := aired.Sub(t); d < -tvdbOneOffWindow || d > tvdbOneOffWindow {
				continue
			}
			if !slices.ContainsFunc(found, func(f TVDBEpisode) bool { return f.Season == e.Season && f.Episode == e.Episode }) {
				found = append(found, e)
			}
		}
	}
	if len(found) != 1 {
		return TVDBEpisode{}, false
	}
	return found[0], true
}

// numberOneOffs gives each one-off of wantName (oneOffTitle; progs[i] is
// results[i]'s programme) the number of the TheTVDB episode matchOneOff
// finds for it, and makes its own title the episode title. It leaves a
// one-off alone when numberFromTVDB has given that number to another
// programme, which answers the search for it already, so the number
// stays as resolve decided. It returns the programmes it numbered.
// Gitea #104.
func (idx tvdbIndex) numberOneOffs(results []bbc.IBLResult, progs []*store.Programme, wantName string) map[*store.Programme]bool {
	taken := map[[2]int]bool{}
	for _, p := range progs {
		if p.TVDBNumbered {
			taken[[2]int{p.Series, p.EpisodeNum}] = true
		}
	}
	numbered := map[*store.Programme]bool{}
	for i, p := range progs {
		title, ok := oneOffTitle(results[i], wantName)
		if !ok {
			continue
		}
		e, ok := idx.matchOneOff(title, p.Name, p.AirDate)
		if !ok || taken[[2]int{e.Season, e.Episode}] {
			continue
		}
		p.Series, p.EpisodeNum, p.Episode = e.Season, e.Episode, title
		p.TVDBNumbered = true
		numbered[p] = true
	}
	return numbered
}

// browseTVDBDeadline bounds the wait for Skyhook on the RSS feed, as
// BrowseFresh and the quality probe bound theirs.
const browseTVDBDeadline = 5 * time.Second

// numberBrowseFromTVDB numbers the RSS feed's episodes of shows Sonarr
// has searched by tvdbid, from the series mappings those searches
// stored. The feed carries no tvdbid, so a programme's candidates are
// the mappings whose show name its brand matches, and "Doctor Who"
// matches Doctor Who (2005) and Doctor Who (2023) alike: each candidate
// resolves every programme that matches it, as a search for it would,
// and a programme takes a candidate's number only when no other
// candidate matches it as strongly, which the air dates see to. A
// programme is left alone when any of its candidates' lists is
// unavailable or empty, and left out if its only number is a guess
// (Gitea #104). An air date alone, or a title matched outside
// tvdbMatchWindow, does not number a programme whose number another
// candidate bears out. A contradicted number is checked against
// the candidate named exactly as the programme goes out (its custom
// name, if an override sets one), the series Sonarr files an unnumbered
// "Brand.S00E04" under.
//
// It returns the mapping each numbered PID took. Its items carry that
// mapping's name and tvdbid, so Sonarr files them under the series whose
// numbers they have.
func (h *Handler) numberBrowseFromTVDB(ctx context.Context, progs []*store.Programme) map[string]*store.SeriesMapping {
	mappings, err := h.store.ListSeriesMappings()
	if err != nil || len(mappings) == 0 {
		return nil
	}
	byName := map[string][]*store.SeriesMapping{}
	candidates := map[*store.Programme][]*store.SeriesMapping{}
	ids := map[string]bool{}
	for _, p := range progs {
		cs, seen := byName[p.Name]
		if !seen {
			for i := range mappings {
				if exactNameMatch(p.Name, mappings[i].ShowName) {
					cs = append(cs, &mappings[i])
				}
			}
			byName[p.Name] = cs
		}
		if len(cs) > 0 {
			candidates[p] = cs
			for _, c := range cs {
				ids[c.TVDBId] = true
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, browseTVDBDeadline)
	defer cancel()
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		indexes = map[string]tvdbIndex{}
		sem     = make(chan struct{}, 4)
	)
	for id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			eps, err := h.episodes.Episodes(ctx, id)
			if err == nil && len(eps) == 0 {
				err = fmt.Errorf("empty episode list")
			}
			if err != nil {
				log.Printf("[browse] TheTVDB episodes for tvdbid=%s unavailable: %v", id, err)
				return
			}
			mu.Lock()
			indexes[id] = newTVDBIndex(eps)
			mu.Unlock()
		}()
	}
	wg.Wait()

	complete := func(p *store.Programme) bool {
		for _, c := range candidates[p] {
			if _, ok := indexes[c.TVDBId]; !ok {
				return false
			}
		}
		return true
	}
	// A programme left alone for want of a list keeps its BBC numbers,
	// but a guessed one is left out of the feed: Sonarr's RSS sync grabs
	// whatever maps to a missing episode, and Joy to the World as
	// "Doctor.Who.S01E04" maps to Aliens of London. It is back at the
	// first poll after the list is. Gitea #104.
	markUnchecked(progs, func(p *store.Programme) bool {
		_, ok := candidates[p]
		return ok && !complete(p)
	})
	// borneOut reports whether a candidate other than the one claiming
	// bears out the number the programme goes out with.
	borneOut := func(p *store.Programme, claimant *store.SeriesMapping) bool {
		s, e, ok := givenNumber(p)
		if !ok {
			return false
		}
		for _, c := range candidates[p] {
			if v := indexes[c.TVDBId].checkGuess(s, e, p.Episode, p.AirDate); c != claimant && (v == guessConfirmed || v == guessNear) {
				return true
			}
		}
		return false
	}
	// goesOutAs is the name an unnumbered programme's items carry.
	goesOutAs := func(p *store.Programme) string {
		if o, _ := h.store.GetOverride(p.Name); o != nil && o.CustomName != "" {
			return o.CustomName
		}
		return p.Name
	}
	type pick struct {
		claim  tvdbClaim
		series *store.SeriesMapping
	}
	picks := map[*store.Programme][]pick{}
	for i := range mappings {
		c := &mappings[i]
		idx, ok := indexes[c.TVDBId]
		if !ok {
			continue
		}
		eligible := func(p *store.Programme) bool {
			return complete(p) && exactNameMatch(p.Name, c.ShowName)
		}
		won, contradicted := idx.resolve(progs, eligible, eligible)
		for _, w := range won {
			picks[w.prog] = append(picks[w.prog], pick{claim: w, series: c})
		}
		for _, p := range contradicted {
			if strings.EqualFold(sanitiseForTitle(goesOutAs(p)), sanitiseForTitle(c.ShowName)) {
				p.TVDBContradicted = true
			}
		}
	}
	out := map[string]*store.SeriesMapping{}
	for p, pk := range picks {
		best := matchedOffWindow
		for _, x := range pk {
			best = max(best, x.claim.strength)
		}
		var chosen []pick
		for _, x := range pk {
			if x.claim.strength == best {
				chosen = append(chosen, x)
			}
		}
		if len(chosen) != 1 || best <= matchedOnAirDate && borneOut(p, chosen[0].series) {
			continue
		}
		chosen[0].claim.apply()
		p.TVDBContradicted = false
		out[p.PID] = chosen[0].series
	}
	log.Printf("[browse] %d episodes numbered from TheTVDB", len(out))
	return out
}

var (
	// reTitleSeriesPrefix is BBC's "Series 7: 6. " or "Season 1: 1. "
	// numbering prefix. QI's series are letters ("Series W: 16. ").
	reTitleSeriesPrefix = regexp.MustCompile(`(?i)^(?:Series|Season)\s+\w+:\s*\d+\.\s*`)
	reLeadingEpisodeNum = regexp.MustCompile(`^\d+\.\s*`)

	// reSpecialsLabel is a "Label: " that groups specials, whose
	// remainder is the special's own title: "Christmas Special: ",
	// "Specials: ", "2020 Specials: ".
	reSpecialsLabel = regexp.MustCompile(`(?i)\b(specials?|christmas|easter|new year|halloween|bonfire)\b`)

	// reSeriesLabel is a bare "Series 23: " or "Season 2: " label, whose
	// remainder is the episode's title ("Series 23: Week 4 Results").
	reSeriesLabel = regexp.MustCompile(`(?i)^(?:series|season)\s+\w+$`)

	// reStoryPartTitle is a part of a story, "The Tenth Planet: Episode
	// 1" or "The Power of the Daleks: Episode 1 (Animated)", which
	// TheTVDB titles "The Tenth Planet (1)". Gitea #104.
	reStoryPartTitle = regexp.MustCompile(`(?i)^(.*\S):\s*(?:episode|part)\s+(\d+|one|two|three|four|five|six|seven|eight|nine|ten)(?:\s+\((?:animated|reconstruction)\))?$`)

	// reEpisodePart is the "Episode 1" that BBC data reads an episode
	// number from (bbc.reEpisodeNum's named form).
	reEpisodePart = regexp.MustCompile(`(?i)^(?:episode|pennod)\s+\d+`)

	// reVersionSuffix marks another version of a programme iPlayer also
	// lists: "At Christmas 2025 - Signed", "The Underwater Menace: Episode
	// 2 (Animated)".
	reVersionSuffix = regexp.MustCompile(`(?: - Signed| \((?:Animated|Reconstruction)\))$`)

	// episodeNumberWords lets "The End of Time - Part One" match
	// TheTVDB's "The End of Time (1)" once "part" is dropped.
	episodeNumberWords = map[string]string{
		"one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
		"six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10",
	}

	// genericTitleWords are words that say nothing about which episode
	// a title is. What follows a "Label: " prefix only counts as a title
	// if it has a word outside this set: "Beyond the Barn: Episode 8"
	// must not match The Repair Shop's own "Episode 8".
	genericTitleWords = map[string]bool{
		"episode": true, "week": true, "day": true, "series": true,
		"season": true, "special": true, "specials": true, "christmas": true,
		"the": true, "a": true, "an": true, "of": true, "and": true,
	}
)

type titleCandidate struct {
	key  string
	weak bool
}

// episodeTitleCandidates returns the normalised titles an iPlayer
// subtitle may carry: the whole subtitle, the subtitle without its
// "Series N: M. " numbering, and, as a weak candidate, what follows a
// label that groups specials ("Christmas Special: No Way Out"), names a
// series ("Series 23: Week 4 Results") or numbers the episode within it
// ("Miracle Day: 1. The New World", Red Dwarf's "II: 1. Kryten"). Any
// other label may be an extra about the episode ("Prequel: The Bells of
// Saint John"), so it is not stripped. Each of these that is a part of a
// story ("The Tenth Planet: Episode 1") is also a candidate as TheTVDB
// titles it, "the tenth planet 1", as "Survival: Part 3" already is once
// episodeTitleKey drops "part".
func episodeTitleCandidates(subtitle string) []titleCandidate {
	var out []titleCandidate
	add := func(k string, weak bool) {
		if k == "" {
			return
		}
		for _, have := range out {
			if have.key == k {
				return
			}
		}
		out = append(out, titleCandidate{key: k, weak: weak})
	}
	addTitle := func(s string, weak bool) {
		add(episodeTitleKey(s), weak)
		if m := reStoryPartTitle.FindStringSubmatch(s); m != nil && specificTitleKey(episodeTitleKey(m[1])) {
			add(episodeTitleKey(m[1]+" "+m[2]), weak)
		}
	}
	addTitle(subtitle, false)
	addTitle(reTitleSeriesPrefix.ReplaceAllString(subtitle, ""), false)
	if label, rest, ok := strings.Cut(subtitle, ": "); ok {
		if reLeadingEpisodeNum.MatchString(rest) || reSpecialsLabel.MatchString(label) || reSeriesLabel.MatchString(label) {
			rest = reLeadingEpisodeNum.ReplaceAllString(rest, "")
			if specificTitleKey(episodeTitleKey(rest)) {
				addTitle(rest, true)
			}
		}
	}
	return out
}

// episodeTitleKey normalises a title for comparison: ASCII-folded, lower
// case, "&" as "and", punctuation dropped, "part" dropped, the number
// words one to ten as digits and leading zeros dropped ("Tardisode 01").
func episodeTitleKey(s string) string {
	s = strings.ToLower(titleTransliterator.Replace(s))
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.ReplaceAll(s, "'", "")
	words := strings.FieldsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := words[:0]
	for _, w := range words {
		if w == "part" {
			continue
		}
		if d, ok := episodeNumberWords[w]; ok {
			w = d
		}
		if strings.Trim(w, "0123456789") == "" {
			if w = strings.TrimLeft(w, "0"); w == "" {
				w = "0"
			}
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

func specificTitleKey(k string) bool {
	for _, w := range strings.Fields(k) {
		if !genericTitleWords[w] && strings.Trim(w, "0123456789") != "" {
			return true
		}
	}
	return false
}
