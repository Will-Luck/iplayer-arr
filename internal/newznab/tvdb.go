package newznab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

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

// tvdbIndex is a series' dated TheTVDB episodes by normalised title, by
// number and, for specials, by air date.
type tvdbIndex struct {
	byTitle    map[string][]TVDBEpisode
	byNumber   map[[2]int]TVDBEpisode
	specialsOn map[string][]TVDBEpisode
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
		if e.Season == 0 {
			day := aired.Format("2006-01-02")
			idx.specialsOn[day] = append(idx.specialsOn[day], e)
		}
	}
	return idx
}

// How a programme matched a TheTVDB episode, weakest first. A match on
// what follows a grouping label could be an extra about the episode, and
// one on the air date alone could be anything else shown that day.
const (
	matchedOnAirDate = iota
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
// fits more than one TheTVDB episode. When programmes with different
// PIDs claim one episode, a signed version ("<subtitle> - Signed") gives
// way to the version it signs, a weaker claim gives way to a stronger
// one, and claims below a full title match keep the number only if their
// subtitles are the same (one programme listed twice). Daily soaps whose
// subtitle is the date keep their date titles.
//
// It returns the claims that keep their numbers, and the specials left
// without one whose guessed number TheTVDB has for an episode aired more
// than tvdbMatchWindow away.
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
			s, e, guessed := positionGuess(p)
			if !guessed {
				continue
			}
			verdict := idx.checkGuess(s, e, p.Episode, p.AirDate)
			if verdict == guessConfirmed || verdict == guessNear && s != 0 {
				continue
			}
			var ok bool
			if m, ok = idx.specialOn(p.Episode, p.AirDate); !ok {
				continue
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
		signs := func(c tvdbClaim) bool {
			base, ok := strings.CutSuffix(c.prog.Episode, " - Signed")
			return ok && claimed[base]
		}
		var kept []tvdbClaim
		for _, c := range cs {
			if !signs(c) {
				kept = append(kept, c)
			}
		}
		cs = kept
		pids := map[string]bool{}
		subtitles := map[int]map[string]bool{}
		best := matchedOnAirDate
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
	for _, p := range considered {
		if numbered[p] || !isSpecial(p.Episode) {
			continue
		}
		if s, e, guessed := positionGuess(p); guessed && idx.checkGuess(s, e, p.Episode, p.AirDate) == guessContradicted {
			contradicted = append(contradicted, p)
		}
	}
	return won, contradicted
}

// numberFromTVDB gives the programmes eligible accepts TheTVDB's numbers
// as resolve decides, marks those whose guess TheTVDB contradicts, and
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
// unavailable or empty. An air date alone does not number a programme
// whose guess another candidate bears out. A guess is checked against
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
	// borneOut reports whether a candidate other than the one claiming
	// bears out the programme's guess.
	borneOut := func(p *store.Programme, claimant *store.SeriesMapping) bool {
		s, e, guessed := positionGuess(p)
		if !guessed {
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
		best := matchedOnAirDate
		for _, x := range pk {
			best = max(best, x.claim.strength)
		}
		var chosen []pick
		for _, x := range pk {
			if x.claim.strength == best {
				chosen = append(chosen, x)
			}
		}
		if len(chosen) != 1 || best == matchedOnAirDate && borneOut(p, chosen[0].series) {
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
// Saint John"), so it is not stripped.
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
	add(episodeTitleKey(subtitle), false)
	add(episodeTitleKey(reTitleSeriesPrefix.ReplaceAllString(subtitle, "")), false)
	if label, rest, ok := strings.Cut(subtitle, ": "); ok {
		if reLeadingEpisodeNum.MatchString(rest) || reSpecialsLabel.MatchString(label) || reSeriesLabel.MatchString(label) {
			if k := episodeTitleKey(reLeadingEpisodeNum.ReplaceAllString(rest, "")); specificTitleKey(k) {
				add(k, true)
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
