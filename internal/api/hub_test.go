package api

import (
	"testing"
	"time"
)

func TestHubBroadcast(t *testing.T) {
	h := NewHub()

	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	h.Broadcast("download:progress", map[string]interface{}{"id": "test1", "progress": 50.0})

	select {
	case evt := <-ch:
		if evt.Type != "download:progress" {
			t.Errorf("type = %q", evt.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("no event received")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()
	h.Unsubscribe(ch)

	// should not panic
	h.Broadcast("test", nil)
}
