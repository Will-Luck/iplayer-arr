package newznab

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type GrabInfo struct {
	PID     string
	Quality string
	Version string
}

// EncodeGUID packs (pid, quality, version) into a stateless GUID. The
// payload is a URL-encoded query string ("p=...&q=...&v=...") inside
// base64. Earlier versions used a colon-separated layout
// (pid:quality:version) parsed with SplitN(":",3), which silently
// misparsed any iBL version field containing a literal ":" (e.g.
// "original:audiodescribed"). Encoding through net/url forces every
// component to round-trip through a parser that escapes delimiters.
// Audit finding item 17.
func EncodeGUID(pid, quality, version string) string {
	v := url.Values{}
	v.Set("p", pid)
	v.Set("q", quality)
	v.Set("v", version)
	return base64.URLEncoding.EncodeToString([]byte(v.Encode()))
}

// DecodeGUID accepts both the modern url.Values payload and the
// legacy colon-separated payload so previously-issued NZBs cached
// by Sonarr continue to resolve. The legacy path uses SplitN(":",3)
// and is still wrong for colon-bearing versions; those grabs were
// already broken in production, the modern path prevents future
// occurrences.
func DecodeGUID(guid string) (*GrabInfo, error) {
	data, err := base64.URLEncoding.DecodeString(guid)
	if err != nil {
		return nil, fmt.Errorf("invalid GUID encoding")
	}
	raw := string(data)
	if v, parseErr := url.ParseQuery(raw); parseErr == nil && v.Get("p") != "" && v.Get("q") != "" {
		return &GrabInfo{
			PID:     v.Get("p"),
			Quality: v.Get("q"),
			Version: v.Get("v"),
		}, nil
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid GUID format")
	}
	return &GrabInfo{PID: parts[0], Quality: parts[1], Version: parts[2]}, nil
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	info, err := DecodeGUID(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><error code="300" description="Item not found"/>`))
		return
	}

	downloadID := fmt.Sprintf("%s:%s", info.PID, info.Quality)

	nzb := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb>
  <head><meta type="name">iParr Internal</meta></head>
  <file subject="iParr download">
    <groups><group>iparr.internal</group></groups>
    <segments><segment number="1">%s</segment></segments>
  </file>
</nzb>`, downloadID)

	w.Header().Set("Content-Type", "application/x-nzb")
	w.Write([]byte(nzb))
}
