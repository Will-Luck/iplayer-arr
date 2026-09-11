package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func seedMoveQueue(t *testing.T, st *store.Store) {
	t.Helper()
	base := time.Date(2026, 8, 25, 16, 20, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		dl := &store.Download{
			ID: "iparr_e" + string(rune('0'+i)), PID: "b00" + string(rune('0'+i)),
			ShowName: "Waterloo Road", Season: 1, Episode: i,
			Status: store.StatusPending, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := st.PutDownload(dl); err != nil {
			t.Fatal(err)
		}
	}
}

func moveReq(id, to string) *http.Request {
	return authedRequest("POST", "/api/downloads/"+id+"/move", strings.NewReader(`{"to":"`+to+`"}`))
}

// GitHub #51 second half: the dashboard can reorder the pending queue.
func TestMoveDownload_ReordersAndReturnsQueue(t *testing.T) {
	h, st := testAPI(t)
	seedMoveQueue(t, st)

	sub := h.hub.Subscribe()
	defer h.hub.Unsubscribe(sub)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, moveReq("iparr_e3", "top"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var queue []store.Download
	if err := json.Unmarshal(w.Body.Bytes(), &queue); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(queue) != 3 || queue[0].ID != "iparr_e3" || queue[1].ID != "iparr_e1" || queue[2].ID != "iparr_e2" {
		t.Fatalf("queue order = %v", func() []string {
			out := []string{}
			for _, d := range queue {
				out = append(out, d.ID)
			}
			return out
		}())
	}

	// The claim order the worker reads agrees.
	all, _ := st.ListDownloads()
	if all[0].ID != "iparr_e3" {
		t.Errorf("ListDownloads first = %s, want iparr_e3", all[0].ID)
	}

	// Other dashboards learn about it over SSE.
	select {
	case ev := <-sub:
		if ev.Type != "queue:reordered" {
			t.Errorf("event type = %q, want queue:reordered", ev.Type)
		}
	case <-time.After(time.Second):
		t.Error("no queue:reordered event broadcast")
	}
}

func TestMoveDownload_RejectsBadInput(t *testing.T) {
	h, st := testAPI(t)
	seedMoveQueue(t, st)

	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"unknown direction", moveReq("iparr_e1", "sideways"), http.StatusBadRequest},
		{"bad json", authedRequest("POST", "/api/downloads/iparr_e1/move", strings.NewReader("{")), http.StatusBadRequest},
		{"unknown id", moveReq("iparr_nope", "top"), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, tc.req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d; body %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// A row a worker has already claimed cannot be moved: its place in the
// queue no longer decides anything, and pretending otherwise would race
// the download in flight.
func TestMoveDownload_RefusesInFlight(t *testing.T) {
	h, st := testAPI(t)
	seedMoveQueue(t, st)
	dl, _ := st.GetDownload("iparr_e2")
	dl.Status = store.StatusDownloading
	st.PutDownload(dl)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, moveReq("iparr_e2", "top"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", w.Code, w.Body.String())
	}
}

func TestMoveDownload_RequiresAuth(t *testing.T) {
	h, st := testAPI(t)
	seedMoveQueue(t, st)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/downloads/iparr_e1/move", strings.NewReader(`{"to":"top"}`))
	req.Header.Set("Origin", "http://"+req.Host) // pass CSRF, fail auth
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
