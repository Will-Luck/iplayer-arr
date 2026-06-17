package bbc

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"time"
)

// GeoStatus is the classified result of the BBC access probe.
type GeoStatus string

const (
	GeoUKOK       GeoStatus = "uk_ok"
	GeoNotUK      GeoStatus = "not_uk"
	GeoDNSFailed  GeoStatus = "dns_failed"
	GeoProbeError GeoStatus = "probe_error"
)

// GeoResult is a classified geo-probe outcome with a human-readable detail.
type GeoResult struct {
	Status GeoStatus
	Detail string
}

// CheckGeo classifies BBC iPlayer access into one of four states. It resolves
// the mediaselector host first (so a resolver failure reads as dns_failed rather
// than a geo-block), then GETs the live probe URL and classifies the body using
// the same geo markers the download path uses. Bounded so a dead tunnel cannot
// stall startup.
func (ms *MediaSelector) CheckGeo(ctx context.Context) GeoResult {
	host := ""
	if u, err := url.Parse(ms.BaseURL); err == nil {
		host = u.Hostname()
	}

	dnsCtx, cancelDNS := context.WithTimeout(ctx, 5*time.Second)
	_, err := ms.lookupIP(dnsCtx, host)
	cancelDNS()
	if err != nil {
		return GeoResult{Status: GeoDNSFailed, Detail: err.Error()}
	}

	getCtx, cancelGet := context.WithTimeout(ctx, 15*time.Second)
	defer cancelGet()
	reqURL := fmt.Sprintf("%s/6/select/version/2.0/mediaset/pc/vpid/bbc_one_hd/format/xml", ms.BaseURL)
	body, err := ms.client.GetCtx(getCtx, reqURL)
	if err != nil {
		return GeoResult{Status: GeoProbeError, Detail: err.Error()}
	}
	if isGeoBlockedBody(body) {
		return GeoResult{Status: GeoNotUK, Detail: "geo-blocked: non-UK exit"}
	}
	var sel mediaSelectionXML
	if err := xml.Unmarshal(body, &sel); err != nil || sel.Error != nil || len(sel.Media) == 0 {
		return GeoResult{Status: GeoProbeError, Detail: "unexpected mediaselector response"}
	}
	return GeoResult{Status: GeoUKOK}
}
