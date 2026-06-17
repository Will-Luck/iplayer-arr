package bbc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckGeo(t *testing.T) {
	const ukXML = `<mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection">` +
		`<media kind="video"><connection href="https://x/y" transferFormat="hls"/></media></mediaSelection>`
	const geoXML = `<mediaSelection><error id="geolocation"/></mediaSelection>`

	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    GeoStatus
	}{
		{"uk_ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(ukXML)) }, GeoUKOK},
		{"not_uk", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(geoXML)) }, GeoNotUK},
		{"probe_error_5xx", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, GeoProbeError},
		{"probe_error_unparseable", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not xml")) }, GeoProbeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			ms := NewMediaSelector(NewClient())
			ms.BaseURL = srv.URL
			got := ms.CheckGeo(context.Background())
			if got.Status != tc.want {
				t.Errorf("Status = %q, want %q (detail %q)", got.Status, tc.want, got.Detail)
			}
		})
	}

	t.Run("dns_failed", func(t *testing.T) {
		ms := NewMediaSelector(NewClient())
		ms.BaseURL = "https://open.live.bbc.co.uk/mediaselector"
		ms.lookupIP = func(_ context.Context, _ string) ([]net.IP, error) {
			return nil, fmt.Errorf("server misbehaving")
		}
		got := ms.CheckGeo(context.Background())
		if got.Status != GeoDNSFailed {
			t.Errorf("Status = %q, want dns_failed", got.Status)
		}
	})
}
