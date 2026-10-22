package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// GitHub #57 fixtures live with the bbc package's other real captures.
const bbcFixtureDir = "../bbc/testdata/ibl/"

func readBBCFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(bbcFixtureDir + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func newGH57Manager(t *testing.T, iblURL, msURL string, opts ...ManagerOption) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	bbcClient := bbc.NewClient()
	playlist := bbc.NewPlaylistResolver(bbcClient)
	playlist.BaseURL = iblURL
	ms := bbc.NewMediaSelector(bbcClient)
	ms.BaseURL = msURL
	return NewManager(st, filepath.Join(dir, "downloads"), 1, bbcClient, playlist, ms, nil, opts...), st
}

func newMetadataFailureManager(t *testing.T, now time.Time) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil,
		WithClock(func() time.Time { return now })), st
}

// A metadata failure says the BBC could not tell us about the episode,
// not that the episode is broken, so it gets the same steady cadence as
// not-yet-available and leaves the CDN counters alone. GitHub #57.
func TestFailDownload_MetadataUnavailableHasOwnBudget(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m, st := newMetadataFailureManager(t, now)

	dl := &store.Download{ID: "md1", PID: "m0033jgh", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	metaErr := errors.New("playlist resolve: fetch episode: BBC returned an HTML page instead of JSON")
	m.failDownload(dl, store.FailCodeMetadataUnavailable, metaErr)

	if !dl.Retryable {
		t.Fatal("first metadata failure must be retryable")
	}
	if dl.MetadataFailures != 1 {
		t.Errorf("MetadataFailures = %d, want 1", dl.MetadataFailures)
	}
	if dl.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (metadata failures have their own budget)", dl.RetryCount)
	}
	if want := now.Add(notYetAvailableRetryInterval); !dl.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", dl.RetryAfter, want)
	}
	if dl.CDNFailures != 0 || len(dl.FailedSuppliers) != 0 {
		t.Errorf("CDNFailures = %d, FailedSuppliers = %v; a metadata failure must not touch them",
			dl.CDNFailures, dl.FailedSuppliers)
	}

	for i := 2; i <= maxMetadataFailures; i++ {
		m.failDownload(dl, store.FailCodeMetadataUnavailable, metaErr)
		if wantRetryable := i < maxMetadataFailures; dl.Retryable != wantRetryable {
			t.Fatalf("after %d metadata failures Retryable = %v, want %v", i, dl.Retryable, wantRetryable)
		}
	}
	if hist, _ := st.GetHistory(dl.ID); hist == nil {
		t.Error("an exhausted metadata budget must move the row to history")
	}
}

// A row that already burned not-yet-available attempts (RetryCount past
// maxRetries) must still get its full metadata budget: with a shared
// counter one BBC blip would fail it permanently.
func TestFailDownload_MetadataBudgetIgnoresSpentNYAAttempts(t *testing.T) {
	m, st := newMetadataFailureManager(t, time.Now())

	dl := &store.Download{
		ID: "md2", PID: "m0033jgh", Quality: "1080p",
		Status:      store.StatusFailed,
		FailureCode: store.FailCodeNotYetAvailable,
		RetryCount:  maxRetries + 2,
		Retryable:   true,
	}
	st.PutDownload(dl)

	m.failDownload(dl, store.FailCodeMetadataUnavailable, errors.New("playlist resolve: fetch episode: HTTP 503"))
	if !dl.Retryable {
		t.Fatal("a metadata failure after spent not-yet-available attempts must still be retryable")
	}
	if dl.RetryCount != maxRetries+2 {
		t.Errorf("RetryCount = %d, want %d unchanged", dl.RetryCount, maxRetries+2)
	}
}

// GitHub #57, worker level: the BBC's HTML error page lands as a
// retryable metadata_unavailable failure whose message says what
// happened.
func TestProcessDownload_GH57_HTMLPageIsMetadataUnavailable(t *testing.T) {
	page := readBBCFixture(t, "bbc_error_page.html")
	errorSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/error.html" {
			http.Redirect(w, r, "/error.html", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(page)
	}))
	t.Cleanup(errorSite.Close)

	m, st := newGH57Manager(t, errorSite.URL, errorSite.URL)
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		t.Fatal("ffmpeg must not run when the episode cannot be resolved")
		return nil
	}

	dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
	m.processDownload(context.Background(), dl)

	if dl.FailureCode != store.FailCodeMetadataUnavailable {
		t.Errorf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeMetadataUnavailable)
	}
	if !dl.Retryable || dl.MetadataFailures != 1 {
		t.Errorf("Retryable = %v, MetadataFailures = %d; want true, 1", dl.Retryable, dl.MetadataFailures)
	}
	for _, want := range []string{"playlist resolve: ", "HTML"} {
		if !strings.Contains(dl.Error, want) {
			t.Errorf("Error %q does not contain %q", dl.Error, want)
		}
	}
	if strings.Contains(dl.Error, "invalid character") {
		t.Errorf("Error %q still reads as a JSON parse failure", dl.Error)
	}
}

// GitHub #57, worker level: the reporter's PID, grabbed with only a pid
// and a quality, resolves through iBL to the version mediaselector takes
// and reaches ffmpeg with the iBL duration and title.
func TestProcessDownload_GH57_ResolvesIssuePIDThroughIBL(t *testing.T) {
	episode := readBBCFixture(t, "episode_m0033jgh.json")
	iblSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/episodes/m0033jgh" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(episode)
	}))
	t.Cleanup(iblSrv.Close)

	media := readBBCFixture(t, "mediaselector_m0033jgj.xml")
	var msPaths []string
	msSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msPaths = append(msPaths, r.URL.Path)
		w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
		w.Write(media)
	}))
	t.Cleanup(msSrv.Close)

	m, st := newGH57Manager(t, iblSrv.URL, msSrv.URL)
	var jobs []FFmpegJob
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		jobs = append(jobs, job)
		return errors.New("ffmpeg: exit status 1")
	}

	id, err := m.Enqueue("m0033jgh", "1080p", "", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload: %v", err)
	}
	m.processDownload(context.Background(), dl)

	if dl.VPID != "m0033jgj" {
		t.Errorf("VPID = %q, want m0033jgj", dl.VPID)
	}
	if dl.Duration != 184 {
		t.Errorf("Duration = %d, want 184", dl.Duration)
	}
	if len(msPaths) == 0 || !strings.Contains(msPaths[0], "/vpid/m0033jgj/") {
		t.Errorf("mediaselector paths = %v, want the iBL version id", msPaths)
	}
	if len(jobs) != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1", len(jobs))
	}
	if jobs[0].Duration != 184*time.Second {
		t.Errorf("job.Duration = %v, want 3m4s", jobs[0].Duration)
	}
	if !strings.Contains(filepath.Base(jobs[0].OutputPath), "UEFA Champions League") {
		t.Errorf("OutputPath = %q, want it named from the iBL title", jobs[0].OutputPath)
	}
}

// End to end with the store re-code: a row the outage failed as
// stream_unavailable blocks a re-grab until it is re-coded, then the
// same pid+quality enqueues fresh. GitHub #57.
func TestEnqueue_GH57_RecodedOutageRowIsSuperseded(t *testing.T) {
	m, st := enqueueHistoryFixture(t)
	dead := &store.Download{
		ID: "iparr_outage57", PID: "m0033jgh", Quality: "1080p", Status: store.StatusFailed,
		FailureCode: store.FailCodeUnavailable,
		Error:       "playlist resolve: parse playlist: invalid character '<' looking for beginning of value",
		CompletedAt: time.Date(2026, 10, 22, 10, 8, 16, 0, time.UTC),
	}
	if err := st.PutHistory(dead); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	if id, _ := m.Enqueue(dead.PID, dead.Quality, "", ""); id != dead.ID {
		t.Fatalf("before the re-code Enqueue returned %q, want the dead row %q", id, dead.ID)
	}
	if _, err := st.RecodeResolverOutageFailures(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecodeResolverOutageFailures: %v", err)
	}
	id, err := m.Enqueue(dead.PID, dead.Quality, "", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == dead.ID {
		t.Fatal("after the re-code Enqueue still returned the dead row")
	}
}

// A cancel during a slow iBL lookup must stop the lookup at once and must
// not write the row back: before, the lookup ran on its own 60s context
// and its eventual metadata failure re-inserted the cancelled row with a
// fresh retry budget. GitHub #57 review.
func TestProcessDownload_GH57_CancelDuringResolveWritesNothingBack(t *testing.T) {
	for _, userCancel := range []bool{true, false} {
		name := "shutdown returns to pending"
		if userCancel {
			name = "user cancel"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(slow.Close)

			m, st := newGH57Manager(t, slow.URL, slow.URL)
			dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
			if userCancel {
				m.MarkCancelled(dl.ID)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			began := time.Now()
			go func() {
				m.processDownload(ctx, dl)
				close(done)
			}()
			<-started
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("processDownload did not return after cancel")
			}
			if took := time.Since(began); took > 2*time.Second {
				t.Errorf("processDownload took %v after cancel; the lookup was not cancelled", took)
			}
			if dl.FailureCode != "" || dl.MetadataFailures != 0 {
				t.Errorf("FailureCode = %q, MetadataFailures = %d; a cancelled lookup must not record a failure",
					dl.FailureCode, dl.MetadataFailures)
			}
			row, _ := st.GetDownload(dl.ID)
			if row == nil {
				t.Fatal("row vanished")
			}
			if userCancel {
				if row.Status == store.StatusFailed || m.IsCancelled(dl.ID) {
					t.Errorf("status = %q, still marked cancelled = %v", row.Status, m.IsCancelled(dl.ID))
				}
			} else if row.Status != store.StatusPending {
				t.Errorf("status = %q, want %q after a shutdown", row.Status, store.StatusPending)
			}
		})
	}
}

// The same for the media-selector step, which ran on a background
// context: a user cancel while it hangs must write nothing back.
func TestProcessDownload_GH57_CancelDuringMediaSelectorWritesNothingBack(t *testing.T) {
	episode := readBBCFixture(t, "episode_m0033jgh.json")
	iblSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(episode)
	}))
	t.Cleanup(iblSrv.Close)

	started := make(chan struct{}, 1)
	slowMS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(slowMS.Close)

	m, st := newGH57Manager(t, iblSrv.URL, slowMS.URL)
	dl := enqueueAndGet(t, m, st, "m0033jgh", "1080p")
	m.MarkCancelled(dl.ID)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.processDownload(ctx, dl)
		close(done)
	}()
	<-started
	began := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("processDownload did not return after cancel")
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("processDownload took %v after cancel; the media-selector lookup was not cancelled", took)
	}
	if dl.Status == store.StatusFailed || dl.FailureCode != "" {
		t.Errorf("Status = %q, FailureCode = %q; a cancelled lookup must not record a failure", dl.Status, dl.FailureCode)
	}
	if m.IsCancelled(dl.ID) {
		t.Error("cancel mark not cleared")
	}
}
