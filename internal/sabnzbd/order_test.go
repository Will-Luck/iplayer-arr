package sabnzbd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/download"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// sonarrGrab is the exact interleaved bulk push captured in production on
// 2026-08-14T00:18, release titles byte-for-byte as logged.
var sonarrGrab = []struct {
	pid   string
	title string
}{
	{"m002ypj4", "Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ynss", "Do.Not.Watch.This.Show.S01E01.Series.1.Frog.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypjh", "Knee.High.Spies.S01E12.Mission.Shhh.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002yntg", "Do.Not.Watch.This.Show.S01E02.Series.1.Vanish.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypj8", "Knee.High.Spies.S01E09.Mission.No.Sleep.Sleepover.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002yntf", "Do.Not.Watch.This.Show.S01E03.Series.1.Parents.1080p.WEB-DL.AAC.H264-iParr"},
}

// postNZB drives one Sonarr grab through the real SABnzbd add endpoint.
//
// Sonarr does NOT send the nzbname query parameter: every production
// handleAdd log line shows nzbname="" for both cat=sonarr and
// cat=movies. The release title arrives as the multipart file's
// filename, which handleAdd falls back to. This helper therefore leaves
// nzbname off entirely so the test exercises the path that actually
// fires.
func postNZB(t *testing.T, h *Handler, pid, quality, releaseTitle string) {
	t.Helper()

	nzbXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<nzb>
  <file subject="%s">
    <groups><group>iparr.internal</group></groups>
    <segments><segment number="1">%s:%s</segment></segments>
  </file>
</nzb>`, releaseTitle, pid, quality)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("name", releaseTitle+".nzb")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(nzbXML)); err != nil {
		t.Fatalf("write NZB: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest("POST", "/sabnzbd/api?mode=addfile&apikey=test-key&cat=sonarr", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Status bool   `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode add response: %v (body %s)", err, w.Body.String())
	}
	if !resp.Status {
		t.Fatalf("add %s failed: %s", releaseTitle, resp.Error)
	}
}

// TestSonarrBulkGrabQueuesInEpisodeOrder is the end-to-end half of the
// GitHub #51 regression: real HTTP requests through the SABnzbd shim,
// backed by a real download.Manager, asserting the queue the worker
// claims from comes back in episode order per show.
func TestSonarrBulkGrabQueuesInEpisodeOrder(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetConfig("api_key", "test-key"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	mgr := download.NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)
	h := NewHandler(st, mgr)

	for _, g := range sonarrGrab {
		postNZB(t, h, g.pid, "1080p", g.title)
	}

	got, err := st.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != len(sonarrGrab) {
		t.Fatalf("queue length = %d, want %d", len(got), len(sonarrGrab))
	}

	want := []string{
		"Do.Not.Watch.This.Show.S01E01.Series.1.Frog.1080p.WEB-DL.AAC.H264-iParr",
		"Do.Not.Watch.This.Show.S01E02.Series.1.Vanish.1080p.WEB-DL.AAC.H264-iParr",
		"Do.Not.Watch.This.Show.S01E03.Series.1.Parents.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E09.Mission.No.Sleep.Sleepover.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E12.Mission.Shhh.1080p.WEB-DL.AAC.H264-iParr",
	}
	gotTitles := make([]string, 0, len(got))
	for _, d := range got {
		gotTitles = append(gotTitles, d.Title)
	}
	for i := range want {
		if gotTitles[i] != want[i] {
			t.Fatalf("SAB queue order wrong at position %d\n got = %v\nwant = %v",
				i, gotTitles, want)
		}
	}
}

// TestSonarrGrabHonoursNzbnameWhenPresent guards the other input shape.
// Production always sends nzbname empty, but the parameter still takes
// precedence in handleAdd when a client does send it, so the identity
// parse has to work from that source too.
func TestSonarrGrabHonoursNzbnameWhenPresent(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetConfig("api_key", "test-key"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	mgr := download.NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)
	h := NewHandler(st, mgr)

	nzbXML := `<?xml version="1.0" encoding="UTF-8"?>
<nzb>
  <file subject="test">
    <groups><group>iparr.internal</group></groups>
    <segments><segment number="1">m002ypj4:1080p</segment></segments>
  </file>
</nzb>`
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("name", "unused.nzb")
	part.Write([]byte(nzbXML))
	mw.Close()

	req := httptest.NewRequest("POST",
		"/sabnzbd/api?mode=addfile&apikey=test-key&cat=sonarr&nzbname=Doctor.Who.S02E05.The.Well.1080p.WEB-DL.AAC.H264-iParr",
		&buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	got, err := st.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("queue length = %d, want 1 (body %s)", len(got), w.Body.String())
	}
	if got[0].ShowName != "Doctor Who" || got[0].Season != 2 || got[0].Episode != 5 {
		t.Errorf("nzbname parse = show %q s%d e%d, want %q s2 e5",
			got[0].ShowName, got[0].Season, got[0].Episode, "Doctor Who")
	}
}
