package download

import (
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// supplierRefusedError marks a failed attempt as a supplier refusal: the
// stream's CDN would not serve it. failDownload records Supplier in
// dl.FailedSuppliers so the next attempt prefers another supplier, and
// does not count the failure toward quality degradation, because a refusal
// says nothing about whether the rendition was too big. GitHub #55.
type supplierRefusedError struct {
	Supplier string
	Reason   string // e.g. "HTTP 403", "connection refused", "DNS failure"
	Err      error
}

func (e *supplierRefusedError) Error() string {
	return fmt.Sprintf("supplier %s refused (%s): %v", supplierLabel(e.Supplier), e.Reason, e.Err)
}

func (e *supplierRefusedError) Unwrap() error { return e.Err }

// ffmpegHTTP4xxRe matches ffmpeg's wording for an HTTP 4xx: the error
// strings from libavutil/error.c ("Server returned 403 Forbidden (access
// denied)", and "Server returned 4XX Client Error, but not one of
// 40{0,1,3,4}" for codes it has no name for, such as 410) and the http
// protocol's "HTTP error 403 Forbidden" line.
var ffmpegHTTP4xxRe = regexp.MustCompile(`(?:Server returned|HTTP error) (4\d\d|4XX)\b`)

// supplierRefusal reports whether a failed ffmpeg run was the supplier
// refusing the stream, and why. That is a *MasterPlaylistError from
// resolveHLSVariant (any non-2xx answer, or the host unreachable), or ffmpeg
// error text showing an HTTP 4xx, a refused connection, or a hostname that
// would not resolve. A watchdog stall is never a refusal, whatever its
// stderr tail says. Any other ffmpeg failure (a 5xx reported by ffmpeg, a
// dropped connection, bad data) keeps the ffmpeg_error quality-degrade
// path.
func supplierRefusal(err error) (reason string, ok bool) {
	if err == nil || errors.Is(err, ErrStalled) {
		return "", false
	}
	var mpErr *MasterPlaylistError
	if errors.As(err, &mpErr) {
		if mpErr.Status != 0 {
			return fmt.Sprintf("HTTP %d", mpErr.Status), true
		}
		return transportFailureReason(mpErr.Err), true
	}

	msg := err.Error()
	if m := ffmpegHTTP4xxRe.FindStringSubmatch(msg); m != nil {
		return "HTTP " + strings.ToLower(m[1]), true
	}
	switch {
	case strings.Contains(strings.ToLower(msg), "connection refused"):
		return "connection refused", true
	case strings.Contains(msg, "Failed to resolve hostname"):
		return "DNS failure", true
	}
	return "", false
}

// transportFailureReason names a Go HTTP transport error for the log.
func transportFailureReason(err error) string {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		return "DNS failure"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	default:
		return "unreachable"
	}
}

// logSupplierRefusal says plainly what the next attempt will do after
// failDownload has recorded a refusal: try another supplier, retry the
// only one(s) on offer, or nothing because the retry budget is spent. The
// prediction re-runs pickStream on this attempt's stream list, which
// mediaselector may reorder by the time of the retry.
func logSupplierRefusal(dl *store.Download, supplier, reason string, streams []bbc.VideoStream) {
	label := supplierLabel(supplier)
	if !dl.Retryable {
		log.Printf("download %s: supplier %s refused (%s), no retries left", dl.ID, label, reason)
		return
	}
	_, nextQual := attemptQuality(dl)
	next := pickStream(streams, nextQual, dl.FailedSuppliers...)
	if next.Supplier == supplier || slices.Contains(dl.FailedSuppliers, next.Supplier) {
		log.Printf("download %s: supplier %s refused (%s); every supplier offering this stream has now refused, so the next attempt will try %s again",
			dl.ID, label, reason, supplierLabel(next.Supplier))
		return
	}
	log.Printf("download %s: supplier %s refused (%s), next attempt will try another supplier", dl.ID, label, reason)
}

// addFailedSupplier appends supplier to failed unless it is already listed
// or unnamed. An unnamed supplier cannot be told apart from another, so it
// is never excluded.
func addFailedSupplier(failed []string, supplier string) []string {
	if supplier == "" || slices.Contains(failed, supplier) {
		return failed
	}
	return append(failed, supplier)
}

func supplierLabel(supplier string) string {
	if supplier == "" {
		return "(unnamed)"
	}
	return supplier
}
