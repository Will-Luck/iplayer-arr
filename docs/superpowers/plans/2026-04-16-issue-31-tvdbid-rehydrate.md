# Issue #31 tvdbid Rehydration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When Sonarr sends a tvsearch with `q=ShowName` and an empty `tvdbid` on episode-level follow-up queries, recover the tvdbid from the store by reverse lookup so every RSS item carries the `<newznab:attr name="tvdbid">` echo that v1.1.5 added.

**Architecture:** The existing `SeriesMapping` store already records `tvdbid -> ShowName` whenever `handleTVSearch` resolves a TVDB id via Skyhook. We add a reverse `ShowName -> SeriesMapping` lookup to the store layer (bucket iteration, case-insensitive name match). `handleTVSearch` calls the reverse lookup when `tvdbid == "" && q != ""` and, on hit, threads the recovered tvdbid through to `writeResultsRSS` so the echo fires. No schema change, no BBC API surface change.

**Tech Stack:** Go 1.23, BoltDB (via `go.etcd.io/bbolt`), standard `testing` + `httptest`. Repo layout: `internal/store/` for storage, `internal/newznab/` for the Newznab handler.

**Out of scope (filed as a separate follow-up issue):** The filter-rejection symptom where Casualty's live production data returns items with `prog.Series`/`prog.EpisodeNum` that do not align with TVDB's season/episode numbering. That one needs its own diagnostic pass to capture the real `Programme` field values and a design decision about whether to bypass the S/E filter when a confirmed tvdbid is present. Doing it in the same PR conflates two unrelated concerns.

**Verified file references (via jcodemunch against git_head `01932fb`):**
- `internal/store/series.go:9` -- `PutSeriesMapping`
- `internal/store/series.go:19` -- `GetSeriesMapping`
- `internal/store/types.go:68` -- `SeriesMapping` struct
- `internal/store/store.go:13` -- `bucketSeries = []byte("series")`
- `internal/store/store_test.go:195` -- `TestSeriesMapping`
- `internal/newznab/search.go:38` -- `handleTVSearch`
- `internal/newznab/search.go:136` -- `writeResultsRSS` (tvdbid attr emitted at the `if tvdbid != ""` block near line 263)
- `internal/newznab/search.go:352` -- `lookupTVDBShow` (emits `[tvsearch] resolved TVDB ... -> ...` log)
- `internal/newznab/handler_test.go:375` -- `TestHandleTVSearchStandardSEStillWorks` (pattern to copy)

**Validation evidence (captured 2026-04-16 against live .57 container `iplayer-arr`):**

```
probe #1 (tvdbid=71756, no q):
  GET /newznab/api?t=tvsearch&tvdbid=71756 -> 8516 bytes, 1 RSS item (Casualty S01E01)
  log: [tvsearch] q="" tvdbid="71756" season="" ep=""
  log: [tvsearch] resolved TVDB 71756 -> "Casualty" (year 1986)
  -> store now holds SeriesMapping{TVDBId:"71756", ShowName:"Casualty", Year:1986}

probe #2 (q=Casualty, tvdbid=, season=45 ep=6):
  GET /newznab/api?t=tvsearch&q=Casualty&tvdbid=&season=45&ep=6 -> 188 bytes, empty RSS
  log: [tvsearch] q="Casualty" tvdbid="" season="45" ep="6"
  -> bug: tvdbid stays empty despite store holding the mapping
```

---

## File Structure

```
internal/store/
  series.go              # MODIFY: add GetSeriesMappingByName
  store_test.go          # MODIFY: add TestGetSeriesMappingByName cases

internal/newznab/
  search.go              # MODIFY: handleTVSearch rehydrates tvdbid when q!="" && tvdbid==""
  handler_test.go        # MODIFY: add TestHandleTVSearch_TVDBIDRehydratedFromStore

CHANGELOG.md             # MODIFY: add v1.1.6 entry under [Unreleased] -> Fixed
```

Responsibilities:
- **`internal/store/series.go`** -- persistence for series mappings. Currently write-by-tvdbid and read-by-tvdbid. Adding read-by-name via bucket iteration (cost is O(n) where n = number of tracked shows, which is typically under a few hundred -- acceptable; if we ever outgrow that, we introduce a secondary name-index bucket).
- **`internal/newznab/search.go`** -- Newznab entry point. Rehydration logic lives in `handleTVSearch` because that's where the tvdbid enters the request pipeline. `writeResultsRSS` signature already accepts `tvdbid string` (added in v1.1.5 for the original echo feature) -- no change.

---

## Task 1: Add reverse-lookup method to the store

**Files:**
- Modify: `internal/store/series.go` (append new method after `GetSeriesMapping` at line 30)
- Test: `internal/store/store_test.go` (append new tests after `TestSeriesMapping` at line 205)

- [ ] **Step 1.1: Write the failing test**

Append to `internal/store/store_test.go` (after the existing `TestSeriesMapping` at line 205):

```go
func TestGetSeriesMappingByName_Found(t *testing.T) {
	s := testStore(t)
	m := &SeriesMapping{TVDBId: "71756", ShowName: "Casualty", Year: 1986}
	if err := s.PutSeriesMapping(m); err != nil {
		t.Fatalf("PutSeriesMapping: %v", err)
	}

	got, err := s.GetSeriesMappingByName("Casualty")
	if err != nil {
		t.Fatalf("GetSeriesMappingByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetSeriesMappingByName returned nil, want mapping")
	}
	if got.TVDBId != "71756" {
		t.Errorf("TVDBId = %q, want %q", got.TVDBId, "71756")
	}
	if got.Year != 1986 {
		t.Errorf("Year = %d, want 1986", got.Year)
	}
}

func TestGetSeriesMappingByName_CaseInsensitive(t *testing.T) {
	s := testStore(t)
	if err := s.PutSeriesMapping(&SeriesMapping{TVDBId: "81797", ShowName: "One Piece", Year: 1999}); err != nil {
		t.Fatalf("PutSeriesMapping: %v", err)
	}

	got, err := s.GetSeriesMappingByName("one piece")
	if err != nil {
		t.Fatalf("GetSeriesMappingByName: %v", err)
	}
	if got == nil || got.TVDBId != "81797" {
		t.Errorf("lowercase lookup failed: got %+v, want TVDBId=81797", got)
	}

	got2, _ := s.GetSeriesMappingByName("ONE PIECE")
	if got2 == nil || got2.TVDBId != "81797" {
		t.Errorf("uppercase lookup failed: got %+v, want TVDBId=81797", got2)
	}
}

func TestGetSeriesMappingByName_NotFound(t *testing.T) {
	s := testStore(t)
	got, err := s.GetSeriesMappingByName("Nothing Here")
	if err != nil {
		t.Fatalf("GetSeriesMappingByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestGetSeriesMappingByName_EmptyInput(t *testing.T) {
	s := testStore(t)
	s.PutSeriesMapping(&SeriesMapping{TVDBId: "71756", ShowName: "Casualty"})

	got, err := s.GetSeriesMappingByName("")
	if err != nil {
		t.Fatalf("GetSeriesMappingByName(empty): %v", err)
	}
	if got != nil {
		t.Errorf("empty input returned %+v, want nil", got)
	}
}

func TestGetSeriesMappingByName_MultipleEntries(t *testing.T) {
	s := testStore(t)
	entries := []*SeriesMapping{
		{TVDBId: "71756", ShowName: "Casualty", Year: 1986},
		{TVDBId: "81797", ShowName: "One Piece", Year: 1999},
		{TVDBId: "78804", ShowName: "Doctor Who", Year: 1963},
	}
	for _, m := range entries {
		if err := s.PutSeriesMapping(m); err != nil {
			t.Fatalf("PutSeriesMapping(%s): %v", m.ShowName, err)
		}
	}

	got, err := s.GetSeriesMappingByName("Doctor Who")
	if err != nil {
		t.Fatalf("GetSeriesMappingByName: %v", err)
	}
	if got == nil || got.TVDBId != "78804" {
		t.Errorf("got %+v, want TVDBId=78804", got)
	}
}
```

- [ ] **Step 1.2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/store/ -run TestGetSeriesMappingByName -v`

Expected: `FAIL` with `s.GetSeriesMappingByName undefined (type *Store has no field or method GetSeriesMappingByName)`.

- [ ] **Step 1.3: Implement `GetSeriesMappingByName`**

Append to `internal/store/series.go` (after the closing brace of `GetSeriesMapping` at line 30):

```go
// GetSeriesMappingByName returns the first SeriesMapping whose ShowName
// matches name (case-insensitive). Returns (nil, nil) when not found.
// Used by the tvsearch handler to rehydrate tvdbid on follow-up queries
// where Sonarr sends q=ShowName with an empty tvdbid.
//
// Cost is O(n) in the number of tracked shows (one bucket scan per call).
// Typical deployments track tens to low hundreds of shows, so the linear
// scan is cheaper than maintaining a secondary name-index bucket. If
// this ever becomes a hot path, add a `bucketSeriesByName` secondary
// index that mirrors writes from PutSeriesMapping.
func (s *Store) GetSeriesMappingByName(name string) (*SeriesMapping, error) {
	if name == "" {
		return nil, nil
	}
	target := strings.ToLower(strings.TrimSpace(name))
	var found *SeriesMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSeries).ForEach(func(_, data []byte) error {
			var m SeriesMapping
			if err := json.Unmarshal(data, &m); err != nil {
				return nil // skip malformed entries, keep scanning
			}
			if strings.ToLower(strings.TrimSpace(m.ShowName)) == target {
				found = &m
				return errStopIteration
			}
			return nil
		})
	})
	if err == errStopIteration {
		err = nil
	}
	return found, err
}

// errStopIteration short-circuits a bolt.Bucket.ForEach once the target
// row is found. Any non-nil return from ForEach's callback ends the
// scan; we use a sentinel so the caller can distinguish "stopped early"
// from a real error.
var errStopIteration = errors.New("iteration stopped")
```

Add the necessary imports to the top of `internal/store/series.go`. The existing file already imports `encoding/json` and `go.etcd.io/bbolt`, so only add what is missing:

```go
import (
	"encoding/json"
	"errors"
	"strings"

	bolt "go.etcd.io/bbolt"
)
```

- [ ] **Step 1.4: Run test to verify it passes**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/store/ -run TestGetSeriesMappingByName -v`

Expected: `PASS` for all four subtests (`_Found`, `_CaseInsensitive`, `_NotFound`, `_EmptyInput`, `_MultipleEntries`).

- [ ] **Step 1.5: Verify existing store tests still pass**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/store/...`

Expected: all tests green.

- [ ] **Step 1.6: Commit**

```bash
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
git add internal/store/series.go internal/store/store_test.go
git commit -m "store: add GetSeriesMappingByName reverse lookup

Linear scan over bucketSeries with case-insensitive name match. Used
by the tvsearch handler to rehydrate tvdbid when Sonarr sends q= only
(issue #31)."
```

---

## Task 2: Rehydrate tvdbid in `handleTVSearch`

**Files:**
- Modify: `internal/newznab/search.go:38-108` (`handleTVSearch`)
- Test: `internal/newznab/handler_test.go` (append after `TestHandleTVSearchStandardSEStillWorks` at line 399)

- [ ] **Step 2.1: Write the failing test**

Append to `internal/newznab/handler_test.go` (after line 399):

```go
// TestHandleTVSearch_TVDBIDRehydratedFromStore covers GitHub issue #31:
// Sonarr sends q=ShowName with an empty tvdbid on episode-level follow-up
// queries. Before the fix, the resulting RSS items had no tvdbid attr and
// Sonarr could not match them back to the series. After the fix, the
// handler does a reverse store lookup and threads the tvdbid through to
// writeResultsRSS.
func TestHandleTVSearch_TVDBIDRehydratedFromStore(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)

	// Seed the store as if an earlier tvdbid=78804 request had resolved.
	if err := h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "78804", ShowName: "Doctor Who", Year: 2005,
	}); err != nil {
		t.Fatalf("seed PutSeriesMapping: %v", err)
	}

	// Sonarr's follow-up request: q filled in, tvdbid empty.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `<newznab:attr name="tvdbid" value="78804"`) {
		t.Errorf("RSS missing rehydrated tvdbid attr.\nbody:\n%s", body)
	}
}

// TestHandleTVSearch_TVDBIDRehydrationCaseInsensitive covers the
// case-insensitive path of the reverse lookup.
func TestHandleTVSearch_TVDBIDRehydrationCaseInsensitive(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "m002pwlf", "type": "episode", "title": "Casualty", "subtitle": "Series 1: 1. Learning Curve", "release_date": "1986-09-06", "parent_position": 1}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "71756", ShowName: "Casualty", Year: 1986,
	})

	// Note lower-case q in the request -- mapping is stored with title-case.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=casualty&tvdbid=&season=1&ep=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(),
		`<newznab:attr name="tvdbid" value="71756"`) {
		t.Errorf("case-insensitive rehydration missed.\nbody:\n%s", w.Body.String())
	}
}

// TestHandleTVSearch_TVDBIDNoRehydrationWhenUnknown verifies we do not
// invent a tvdbid when the store has no mapping for the requested show.
func TestHandleTVSearch_TVDBIDNoRehydrationWhenUnknown(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	// deliberately no PutSeriesMapping

	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), `<newznab:attr name="tvdbid"`) {
		t.Errorf("tvdbid attr emitted without a store mapping.\nbody:\n%s",
			w.Body.String())
	}
}

// TestHandleTVSearch_TVDBIDRequestParamWinsOverStore covers the shape
// where Sonarr sends a tvdbid explicitly -- the store lookup must not
// override it, even if the store happens to have a different entry
// for the same show name.
func TestHandleTVSearch_TVDBIDRequestParamWinsOverStore(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	// Store has a STALE mapping (wrong tvdbid for this name).
	h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "99999", ShowName: "Doctor Who", Year: 2005,
	})

	// Request supplies the correct tvdbid.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=78804&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `<newznab:attr name="tvdbid" value="78804"`) {
		t.Errorf("expected request tvdbid=78804 to win, body:\n%s", body)
	}
	if strings.Contains(body, `value="99999"`) {
		t.Errorf("store tvdbid=99999 leaked over request tvdbid=78804, body:\n%s", body)
	}
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/newznab/ -run TestHandleTVSearch_TVDBID -v`

Expected: `FAIL` -- the first three tests fail because no tvdbid attr is emitted when the request sends an empty tvdbid. The fourth (`_RequestParamWinsOverStore`) should already pass on master because the request param is trusted as-is -- treat that as a safety net against accidentally always-overwriting in the next step.

- [ ] **Step 2.3: Implement the rehydration block in `handleTVSearch`**

Modify `internal/newznab/search.go` -- locate the existing block at line 47:

```go
	var filterYear int
	if q == "" && tvdbid != "" {
```

Insert a NEW block IMMEDIATELY BEFORE it (new lines start at what is currently line 47). The new block mirrors the shape of the existing `q==""` block but with the branches swapped:

```go
	// Issue #31: Sonarr sends q=ShowName with an empty tvdbid on
	// episode-level follow-up queries (after the initial tvdbid-only
	// lookup warmed the store). Recover the tvdbid from the store so
	// the <newznab:attr name="tvdbid"> echo in writeResultsRSS still
	// fires on these follow-ups. The request's own tvdbid parameter
	// always wins -- we only rehydrate when tvdbid == "".
	var filterYear int
	if q != "" && tvdbid == "" && h.store != nil {
		cached, _ := h.store.GetSeriesMappingByName(q)
		if cached != nil {
			tvdbid = cached.TVDBId
			if cached.Year > 0 {
				filterYear = cached.Year
			}
			log.Printf("[tvsearch] rehydrated tvdbid=%q for q=%q from store",
				tvdbid, q)
		}
	}
	if q == "" && tvdbid != "" {
```

Because `filterYear` is now declared in the new block, REMOVE the duplicate `var filterYear int` inside the existing block. The final shape of the surrounding code should be:

```go
	log.Printf("[tvsearch] q=%q tvdbid=%q season=%q ep=%q", q, tvdbid, seasonStr, epStr)

	// Issue #31: Sonarr sends q=ShowName with an empty tvdbid on
	// episode-level follow-up queries (after the initial tvdbid-only
	// lookup warmed the store). Recover the tvdbid from the store so
	// the <newznab:attr name="tvdbid"> echo in writeResultsRSS still
	// fires on these follow-ups. The request's own tvdbid parameter
	// always wins -- we only rehydrate when tvdbid == "".
	var filterYear int
	if q != "" && tvdbid == "" && h.store != nil {
		cached, _ := h.store.GetSeriesMappingByName(q)
		if cached != nil {
			tvdbid = cached.TVDBId
			if cached.Year > 0 {
				filterYear = cached.Year
			}
			log.Printf("[tvsearch] rehydrated tvdbid=%q for q=%q from store",
				tvdbid, q)
		}
	}
	if q == "" && tvdbid != "" {
		// Try stored mapping first - but only use the warm cache if it
		// has a year (Year > 0). Old v1.0.2/v1.1.0 records have no year
		// field in the JSON and deserialise to Year=0 - those need to
		// be backfilled by re-hitting Skyhook on first use after the
		// upgrade. See issue #18 and the Phase 4 design doc.
		if h.store != nil {
			cached, _ := h.store.GetSeriesMapping(tvdbid)
			if cached != nil && cached.Year > 0 {
				q = cached.ShowName
				filterYear = cached.Year
			}
		}
		// ... remainder unchanged (Skyhook fallback, PutSeriesMapping, etc.)
	}
```

Do NOT touch any other line of `handleTVSearch`. The existing `q==""` branch, the `h.ibl == nil` check, the `filterName := q` line, the BBC search call, and the `h.writeResultsRSS(..., tvdbid)` invocation all stay identical. The tvdbid variable now arrives at `writeResultsRSS` populated either from the request, from the Skyhook lookup, or from the new reverse store lookup -- `writeResultsRSS` already handles `tvdbid != ""` correctly (line 263 of the current file).

- [ ] **Step 2.4: Run all four tests to verify they pass**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/newznab/ -run TestHandleTVSearch_TVDBID -v`

Expected: all four tests `PASS`.

- [ ] **Step 2.5: Run the whole newznab test suite**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./internal/newznab/...`

Expected: everything green. In particular `TestHandleTVSearchStandardSEStillWorks`, `TestHandleTVSearchDailyMatchByDate`, `TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand`, and `TestMatchesSearchFilter_TableDriven` must all still pass -- we added a pre-filter, not a replacement.

- [ ] **Step 2.6: Commit**

```bash
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
git add internal/newznab/search.go internal/newznab/handler_test.go
git commit -m "newznab: rehydrate tvdbid from store on q-only tvsearch (#31)

When Sonarr sends q=ShowName with an empty tvdbid on episode-level
follow-up queries, do a reverse lookup in bucketSeries to recover
the tvdbid from a prior resolution. The recovered value flows into
writeResultsRSS so the <newznab:attr name='tvdbid'> echo that v1.1.5
added keeps firing across the whole Sonarr query chain.

Request tvdbid still wins when present; the rehydration only runs
when tvdbid is explicitly empty. Year is recovered too so Phase 4
year-disambiguation keeps working for duplicate-brand shows."
```

---

## Task 3: Regression sweep + live-API proof on .57

**Files:** none -- verification only.

- [ ] **Step 3.1: Full test suite from the repo root**

Run: `cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate && go test ./...`

Expected: all packages green. If anything outside `internal/store/` or `internal/newznab/` fails, the change leaked beyond its intended blast radius -- stop and investigate before proceeding.

- [ ] **Step 3.2: Vet + format check**

Run:
```bash
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
go vet ./...
gofmt -l internal/
```

Expected: `go vet` prints nothing; `gofmt -l` prints nothing (no unformatted files).

- [ ] **Step 3.3: Local smoke binary**

Run:
```bash
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
go build -o /tmp/iplayer-arr-issue31 ./cmd/iplayer-arr
echo "built:" && ls -la /tmp/iplayer-arr-issue31
```

Expected: a binary at `/tmp/iplayer-arr-issue31`. Do NOT run it against production data on .57. Next step exercises it end-to-end in a throwaway sandbox before we escalate to a Docker smoke.

- [ ] **Step 3.4: Local runtime test against the compiled binary**

This step exercises the rehydration flow end-to-end: real Skyhook call, real BBC iBL call, real BoltDB write, real HTTP response parse. It catches the class of bugs unit tests cannot see (bucket init order, JSON field tag typos, store-pointer nil in a live handler, port binding).

Binary env-var contract (verified via jcodemunch against `cmd/iplayer-arr/main.go:37-42`): `PORT` (default 62001), `CONFIG_DIR` (default /config), `DOWNLOAD_DIR` (default /downloads). `/newznab/api` has no apikey gate, so curl calls need no auth. Pick port 63099 (memory `smoke_test_pattern.md` range 62000-63999 for .57, 63099 is free per `ss -ltn` on 2026-04-16).

```bash
# fresh sandbox
SMOKE_CONFIG=$(mktemp -d /tmp/iplayer31-config-XXXX)
SMOKE_DL=$(mktemp -d /tmp/iplayer31-downloads-XXXX)
SMOKE_PORT=63099

# start the binary detached, logs to a temp file
SMOKE_LOG=$(mktemp /tmp/iplayer31-log-XXXX.log)
CONFIG_DIR="$SMOKE_CONFIG" DOWNLOAD_DIR="$SMOKE_DL" PORT="$SMOKE_PORT" \
  /tmp/iplayer-arr-issue31 > "$SMOKE_LOG" 2>&1 &
SMOKE_PID=$!
echo "started pid=$SMOKE_PID port=$SMOKE_PORT log=$SMOKE_LOG"

# wait up to 10s for the listening log line
for i in $(seq 1 20); do
  grep -q "listening on :$SMOKE_PORT" "$SMOKE_LOG" && break
  sleep 0.5
done
grep "listening on" "$SMOKE_LOG" || { echo "FAIL: did not start"; kill $SMOKE_PID; exit 1; }

# (1) warm the store: tvdbid-only lookup for Doctor Who (78804)
curl -sf -o /dev/null "http://127.0.0.1:$SMOKE_PORT/newznab/api?t=tvsearch&tvdbid=78804" || {
  echo "FAIL: warm request errored"; kill $SMOKE_PID; exit 1; }
sleep 1
grep -E 'resolved TVDB 78804' "$SMOKE_LOG" || {
  echo "FAIL: warm request did not log resolved TVDB"; kill $SMOKE_PID; exit 1; }

# (2) reproduce issue #31's failing shape: q=Doctor+Who + empty tvdbid + S/E
curl -sf -o /tmp/iplayer31-rss.xml \
  "http://127.0.0.1:$SMOKE_PORT/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3"

# (3) the rehydration log line must appear
grep -E 'rehydrated tvdbid="78804" for q="Doctor Who"' "$SMOKE_LOG" || {
  echo "FAIL: rehydration log missing"; cat "$SMOKE_LOG"; kill $SMOKE_PID; exit 1; }

# (4) the RSS response must carry the tvdbid attr on at least one item
if ! grep -q 'name="tvdbid" value="78804"' /tmp/iplayer31-rss.xml; then
  echo "FAIL: RSS missing tvdbid attr"
  head -c 2000 /tmp/iplayer31-rss.xml
  kill $SMOKE_PID
  exit 1
fi

echo "PASS: local runtime test green"
kill $SMOKE_PID
wait $SMOKE_PID 2>/dev/null
rm -rf "$SMOKE_CONFIG" "$SMOKE_DL" "$SMOKE_LOG" /tmp/iplayer31-rss.xml
```

Expected: script prints `PASS: local runtime test green` and exits 0. Any of the four `FAIL:` branches means something is wrong before we ever push to Docker. Inspect `$SMOKE_LOG` (the script prints it before the kill) and return to Task 2 if the rehydration log line is missing, or Task 1 if the warm request does not log the store write.

Safety notes:
- The script binds to `127.0.0.1:63099`, not `0.0.0.0`, so it is not reachable from the LAN and cannot be mistaken for the production container on port 62932.
- The temp dirs live under `/tmp/iplayer31-*` so they cannot collide with production `/config` or `/downloads` paths.
- The binary is killed with `kill $SMOKE_PID` (SIGTERM). If it hangs, the next manual run-through is `pkill -f /tmp/iplayer-arr-issue31`.

- [ ] **Step 3.5: Isolated smoke test on .57 (per memory `smoke_test_pattern.md`)**

Start a fresh container alongside production using a high random port (63100), a throwaway tmpfs config, and no bind mounts:

```bash
IMG_TMP=iplayer-arr-issue31-smoke:local
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
docker build -t "$IMG_TMP" .
docker run -d --rm --name iplayer-arr-smoke -p 63100:62001 --tmpfs /config "$IMG_TMP"
sleep 5
# warm the store
curl -s "http://192.168.1.57:63100/newznab/api?t=tvsearch&tvdbid=78804" >/dev/null
sleep 2
# reproduce issue #31's failing probe
curl -s -o /tmp/smoke.xml -w "size=%{size_download}\n" \
  "http://192.168.1.57:63100/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3"
grep -c 'name="tvdbid" value="78804"' /tmp/smoke.xml
docker rm -f iplayer-arr-smoke
```

Expected: `grep -c` prints `1` or more (the tvdbid echo fires). If it prints `0`, rehydration did not engage -- stop and re-check Task 2.3.

- [ ] **Step 3.6: No commit -- this task verifies, does not change files**

---

## Task 4: Changelog + issue close plan

**Files:**
- Modify: `CHANGELOG.md` (add a v1.1.6 entry under `[Unreleased] -> Fixed`)

- [ ] **Step 4.1: Append to CHANGELOG.md**

Open `CHANGELOG.md`, find the `## [Unreleased]` section, and add under its `### Fixed` subheading (create the subheading if absent):

```markdown
- **Sonarr follow-up episode searches now carry `tvdbid` attribute (#31)**: When Sonarr sends a tvsearch with `q=ShowName` and an empty `tvdbid` (the shape it uses for episode-level follow-ups after an initial tvdbid-only lookup), iplayer-arr now reverse-looks-up the tvdbid in its series mapping store so the `<newznab:attr name="tvdbid">` echo keeps firing on every item. Previously Sonarr could not match these items back to the correct series for shows with duplicate BBC brand names or where the `q` string alone was ambiguous.
```

- [ ] **Step 4.2: Commit**

```bash
cd /home/lns/iplayer-arr/.worktrees/issue-31-tvdbid-rehydrate
git add CHANGELOG.md
git commit -m "docs: note #31 tvdbid rehydration under v1.1.6 unreleased"
```

- [ ] **Step 4.3: Draft the issue close comment (DO NOT POST YET)**

Save to `/tmp/issue-31-close.md`. Must contain no em dashes, no en dashes, and none of the AI-validation phrases the user bans in their memory feedback files. Self-grep before handing back:

```bash
grep -Pn '[\x{2013}\x{2014}]|\bgenuinely\s+use\w+\b|\bgreat\s+quest\w+\b' /tmp/issue-31-close.md && echo "BANNED" || echo "clean"
```

Draft content (pitch the length to match the reporter's original post, about 4-6 sentences):

```markdown
Hi, thanks for the detailed logs, they pointed straight at the cause. iplayer-arr was dropping the tvdbid attribute on your episode-level tvsearch requests because Sonarr only sends a tvdbid on the first lookup, then switches to `q=Casualty&tvdbid=` for every follow-up. v1.1.6 adds a reverse lookup in the local series mapping store so the tvdbid keeps flowing through to the RSS items on those follow-up queries.

There is a second issue that also affects Casualty and One Piece specifically. BBC's series/episode numbering for long-runners (45+ seasons) does not always match TheTVDB's numbering, so even with the tvdbid fix, some individual episode queries still come back empty because iplayer-arr's internal filter rejects them. That one needs its own design pass and I've opened a separate issue to track it. Please reopen this ticket if v1.1.6 does not improve things for Call the Midwife / HIGNFY-style shows where the numbering does match.
```

---

## Self-Review

**1. Spec coverage:**

- [x] Reverse store lookup by name (Task 1)
- [x] `handleTVSearch` rehydrates tvdbid when `q!=""` and `tvdbid==""` (Task 2.3)
- [x] Request tvdbid still wins (Task 2.1 test `_RequestParamWinsOverStore`, Task 2.3 guard `tvdbid == ""`)
- [x] Year is also recovered for Phase 4 year-disambiguation (Task 2.3 copies `cached.Year` into `filterYear`)
- [x] Case-insensitive match (Task 1.1 and 2.1 both assert this)
- [x] Unknown show does not fabricate a tvdbid (Task 2.1 `_TVDBIDNoRehydrationWhenUnknown`)
- [x] Regression sweep covers existing newznab/store tests (Task 3.1)
- [x] Changelog entry under Unreleased (Task 4.1)
- [x] Issue close comment drafted with self-grep for banned tokens (Task 4.3)

**2. Placeholder scan:**

- No "TBD", "TODO", "implement later", "fill in details" in the plan body.
- No "add appropriate error handling" -- the actual sentinel-based ForEach short-circuit is written out.
- No "write tests for the above" -- each test function is in full.
- No "similar to Task N" -- each task block carries its own verbatim code.

**3. Type consistency:**

- Method: `GetSeriesMappingByName(name string) (*SeriesMapping, error)` -- same name in Task 1 definition, Task 1 tests, Task 2 test seed path, and Task 2 handler call.
- Field names: `TVDBId`, `ShowName`, `Year` -- match existing `SeriesMapping` struct at `internal/store/types.go:68`.
- Variable names: `tvdbid`, `q`, `filterYear`, `h.store`, `h.ibl` -- match existing `handleTVSearch` locals.
- Bucket name: `bucketSeries` -- matches `internal/store/store.go:13`.
- Test helpers: `newHandlerWithBBC`, `h.store.PutSeriesMapping` -- match existing patterns in `internal/newznab/handler_test.go:375` and `internal/store/store_test.go:195`.

Plan passes self-review.
