package newznab

import (
	"encoding/base64"
	"testing"
)

// TestGUID_RoundTrip checks the happy path: encode then decode returns
// the original components.
func TestGUID_RoundTrip(t *testing.T) {
	cases := []struct{ pid, quality, version string }{
		{"b0123", "720p", "original"},
		{"b0123", "1080p", "audiodescribed"},
		{"b0123", "540p", ""},
		{"b!@#$%", "720p", "original"},
	}
	for _, c := range cases {
		guid := EncodeGUID(c.pid, c.quality, c.version)
		info, err := DecodeGUID(guid)
		if err != nil {
			t.Errorf("decode %q (pid=%q): %v", guid, c.pid, err)
			continue
		}
		if info.PID != c.pid || info.Quality != c.quality || info.Version != c.version {
			t.Errorf("round-trip mismatch: got pid=%q q=%q v=%q, want %q %q %q",
				info.PID, info.Quality, info.Version, c.pid, c.quality, c.version)
		}
	}
}

// TestGUID_ColonBearingVersion verifies the modern encoder survives a
// version field containing a literal colon. The legacy colon-split
// decoder rolled the suffix into the version; the modern encoder
// URL-escapes the colon and survives the round-trip. Audit item 17.
func TestGUID_ColonBearingVersion(t *testing.T) {
	guid := EncodeGUID("b0123", "720p", "original:audiodescribed")
	info, err := DecodeGUID(guid)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Version != "original:audiodescribed" {
		t.Errorf("version = %q, want %q", info.Version, "original:audiodescribed")
	}
	if info.PID != "b0123" {
		t.Errorf("pid = %q, want b0123", info.PID)
	}
	if info.Quality != "720p" {
		t.Errorf("quality = %q, want 720p", info.Quality)
	}
}

// TestGUID_LegacyColonFormat verifies the decoder still understands
// pre-v1.5.1 colon-separated GUIDs so Sonarr's NZB cache from earlier
// versions keeps resolving.
func TestGUID_LegacyColonFormat(t *testing.T) {
	legacyRaw := "b0123:720p:original"
	guid := base64.URLEncoding.EncodeToString([]byte(legacyRaw))

	info, err := DecodeGUID(guid)
	if err != nil {
		t.Fatalf("legacy decode: %v", err)
	}
	if info.PID != "b0123" || info.Quality != "720p" || info.Version != "original" {
		t.Errorf("legacy decode mismatch: pid=%q q=%q v=%q", info.PID, info.Quality, info.Version)
	}
}

func TestGUID_InvalidBase64(t *testing.T) {
	if _, err := DecodeGUID("!!!not-base64!!!"); err == nil {
		t.Error("expected error decoding non-base64 input")
	}
}

func TestGUID_MalformedPayload(t *testing.T) {
	guid := base64.URLEncoding.EncodeToString([]byte("only-one-part"))
	if _, err := DecodeGUID(guid); err == nil {
		t.Error("expected error decoding malformed payload")
	}
}
