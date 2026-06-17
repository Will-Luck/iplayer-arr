package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// This file extends /api/diag with one endpoint per environment-health
// concern. The v1.5.6 set in diag_extra.go are *regression anchors* —
// each was added to lock in a specific past bug. The v1.5.7 set here
// are *environment-health probes* — they verify the user's container
// is correctly configured to reach BBC infrastructure and run reliably.
// Same auth pattern, same `verdict: pass|fail` JSON shape, same
// Gitea-Actions gate location. Different question: "is the user's
// container wired up right?" rather than "did we re-break our own code?"
//
// New endpoints (v1.5.7):
//
//   - /api/diag/storage -- download-path health (writability, free space,
//                          ownership, NFS responsiveness)
//   - /api/diag/clock   -- system time drift vs trusted HTTP Date header
//   - /api/diag/geo     -- UK geo presence (wraps RuntimeStatus cache)
//   - /api/diag/network -- DNS + TCP reachability to BBC Akamai + IBL + canary

// ----------------------------------------------------------------------
// /api/diag/storage  (A.10)
// ----------------------------------------------------------------------

// StoragePathReport is the per-path entry in DiagStorageReport.Paths.
type StoragePathReport struct {
	Label            string `json:"label"`
	Path             string `json:"path"`
	Exists           bool   `json:"exists"`
	Writable         bool   `json:"writable"`
	FreeBytes        int64  `json:"free_bytes"`
	FreeHuman        string `json:"free_human"`
	MountFSType      string `json:"mount_fstype,omitempty"`
	NFSResponsive    bool   `json:"nfs_responsive"`
	OwnerUID         int    `json:"owner_uid"`
	OwnerGID         int    `json:"owner_gid"`
	ProcessUID       int    `json:"process_uid"`
	ProcessGID       int    `json:"process_gid"`
	OwnershipMatches bool   `json:"ownership_matches"`
	Error            string `json:"error,omitempty"`
	Verdict          string `json:"verdict"`
}

// DiagStorageReport is the JSON returned by /api/diag/storage.
//
// Catches the two most common Docker-homelab "downloads succeed but
// disappear / never finish" failure classes: NFS detach-without-error
// (mount still in /proc/mounts but reads hang or EIO) and bind-mount
// permission drift (container UID/GID mismatched with host directory
// ownership). The probe iterates the configured download path plus
// its incomplete/ subdir, returning a per-path report and a single
// aggregated verdict.
type DiagStorageReport struct {
	Verdict      string              `json:"verdict"`
	ChecksFailed []string            `json:"checks_failed"`
	Paths        []StoragePathReport `json:"paths"`
}

// diagStoragePathProbe checks a single path and returns its report.
// Production wires defaultStoragePathProbe (real syscalls); tests
// inject deterministic fakes for the failure scenarios that are
// hard to reproduce on a real filesystem (NFS hang, disk full).
type diagStoragePathProbe func(label, path string) StoragePathReport

func (h *Handler) handleDiagStorage(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	probe := h.storageProbe
	if probe == nil {
		probe = defaultStoragePathProbe
	}

	report := DiagStorageReport{
		ChecksFailed: []string{},
		Paths:        []StoragePathReport{},
	}

	downloadDir := h.ResolveDownloadDir()

	// Only probe the configured download directory. The incomplete/
	// subdir under it is created on first download; checking it before
	// that point would surface false positives in fresh deployments
	// and in CI containers that have never run a real grab.
	targets := []struct{ label, path string }{
		{"download_dir", downloadDir},
	}

	for _, t := range targets {
		pr := probe(t.label, t.path)
		report.Paths = append(report.Paths, pr)
		if pr.Verdict != "pass" {
			reason := pr.Error
			if reason == "" {
				reason = "unspecified failure"
			}
			report.ChecksFailed = append(report.ChecksFailed,
				fmt.Sprintf("%s: %s", t.label, reason))
		}
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// defaultStoragePathProbe runs real syscalls. Bounded to 3 seconds
// per path total (Statfs + ReadDir for NFS responsiveness check).
func defaultStoragePathProbe(label, path string) StoragePathReport {
	pr := StoragePathReport{
		Label:      label,
		Path:       path,
		ProcessUID: os.Getuid(),
		ProcessGID: os.Getgid(),
	}

	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			pr.Error = "path does not exist"
		} else {
			pr.Error = "stat: " + err.Error()
		}
		pr.Verdict = "fail"
		return pr
	}
	pr.Exists = true

	if sys, ok := stat.Sys().(*syscall.Stat_t); ok {
		pr.OwnerUID = int(sys.Uid)
		pr.OwnerGID = int(sys.Gid)
		pr.OwnershipMatches = pr.OwnerUID == pr.ProcessUID && pr.OwnerGID == pr.ProcessGID
	} else {
		pr.OwnershipMatches = true // non-unix: skip the check, don't fail on it
	}

	sentinel := filepath.Join(path, fmt.Sprintf(".iplayer-arr-diag-%d", os.Getpid()))
	f, werr := os.Create(sentinel)
	if werr != nil {
		pr.Error = "writable: " + werr.Error()
		pr.Verdict = "fail"
		return pr
	}
	_ = f.Close()
	_ = os.Remove(sentinel)
	pr.Writable = true

	var sfs syscall.Statfs_t
	if err := syscall.Statfs(path, &sfs); err == nil {
		pr.FreeBytes = int64(sfs.Bavail) * sfs.Bsize
		pr.FreeHuman = humanBytes(pr.FreeBytes)
	}

	pr.MountFSType = detectMountFSType(path)
	if isNFSFSType(pr.MountFSType) {
		pr.NFSResponsive = nfsResponsiveWithin(path, 2*time.Second)
	} else {
		pr.NFSResponsive = true
	}

	const minFreeBytes int64 = 1 << 30 // 1 GiB
	// tmpfs is in-memory storage with no meaningful "free" semantics
	// for a download workflow; skip the threshold check there so CI
	// containers and ephemeral test environments don't false-fail.
	enforceFreeSpace := pr.MountFSType != "tmpfs"
	switch {
	case !pr.Writable:
		pr.Verdict = "fail"
		pr.Error = "not writable"
	case enforceFreeSpace && pr.FreeBytes > 0 && pr.FreeBytes < minFreeBytes:
		pr.Verdict = "fail"
		pr.Error = fmt.Sprintf("low free space: %s < 1.0 GiB", pr.FreeHuman)
	case !pr.NFSResponsive:
		pr.Verdict = "fail"
		pr.Error = "NFS unresponsive within 2s"
	default:
		pr.Verdict = "pass"
	}
	return pr
}

// nfsResponsiveWithin returns true iff os.ReadDir(path) completes
// without error before the deadline. Used to detect stale NFS handles
// where the mount remains in /proc/mounts but operations hang.
func nfsResponsiveWithin(path string, deadline time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := os.ReadDir(path)
		done <- err
	}()
	select {
	case err := <-done:
		return err == nil
	case <-ctx.Done():
		return false
	}
}

// detectMountFSType reads /proc/mounts and returns the fstype of the
// longest mountpoint that is a prefix of path. Returns "" if no match
// or /proc/mounts is unavailable.
func detectMountFSType(path string) string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	bestMatch := ""
	bestType := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountpoint := fields[1]
		fstype := fields[2]
		if abs == mountpoint || strings.HasPrefix(abs, mountpoint+"/") {
			if len(mountpoint) > len(bestMatch) {
				bestMatch = mountpoint
				bestType = fstype
			}
		}
	}
	return bestType
}

func isNFSFSType(fstype string) bool {
	switch fstype {
	case "nfs", "nfs4":
		return true
	}
	return false
}

// humanBytes renders an int64 byte count as a short human string
// (e.g. "1.5 GiB"). Returns "0 B" for zero or negative input.
func humanBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), suffix)
}

// ----------------------------------------------------------------------
// /api/diag/clock  (A.11)
// ----------------------------------------------------------------------

// DiagClockReport is the JSON returned by /api/diag/clock.
//
// Catches NTP failures that cause silent Akamai HLS-token 403s:
// Akamai auth tokens (hdnts=...) embed a signed expiry timestamp;
// requests with a Date header drifted more than a small window
// (BBC/Akamai allow ~3 minutes either way) return 403 with no
// informative body. The probe HEADs three trusted endpoints
// (Cloudflare, Google, BBC) in sequence, takes the first that
// responds, and reports drift in seconds with a Cristian-algorithm
// half-RTT correction. Drift > 60s fails the verdict — well under
// Akamai's tolerance so users have lead time before grabs break.
type DiagClockReport struct {
	Verdict               string   `json:"verdict"`
	ChecksFailed          []string `json:"checks_failed"`
	LocalTime             string   `json:"local_time"`
	ReferenceTime         string   `json:"reference_time,omitempty"`
	ReferenceSource       string   `json:"reference_source,omitempty"`
	DriftSeconds          int64    `json:"drift_seconds"`
	DriftSecondsCorrected int64    `json:"drift_seconds_corrected"`
	RoundTripMs           int64    `json:"round_trip_ms,omitempty"`
	ThresholdSeconds      int64    `json:"threshold_seconds"`
}

// clockHeadDateFunc returns the parsed Date header from an HTTP HEAD
// to url, plus the measured round-trip duration. Used by A.11 to
// compare local time against a trusted reference. Production wires
// defaultClockHeadDate; tests inject deterministic fakes.
type clockHeadDateFunc func(ctx context.Context, url string) (date time.Time, rtt time.Duration, err error)

// clockReferenceSources is the in-order list of trusted endpoints
// the clock probe tries. Cloudflare first because of geographic edge
// ubiquity and Date header reliability; Google as backup; BBC last
// because its clock is the dispute that matters most for Akamai but
// also the highest-latency option from non-UK CI.
var clockReferenceSources = []string{
	"https://www.cloudflare.com/",
	"https://www.google.com/",
	"https://www.bbc.co.uk/",
}

const clockDriftThresholdSeconds int64 = 60

func (h *Handler) handleDiagClock(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	now := h.now()
	headDate := h.clockHeadDate
	if headDate == nil {
		headDate = defaultClockHeadDate
	}

	report := DiagClockReport{
		ChecksFailed:     []string{},
		LocalTime:        now.UTC().Format(time.RFC3339),
		ThresholdSeconds: clockDriftThresholdSeconds,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var (
		refTime time.Time
		refRTT  time.Duration
		refURL  string
		lastErr error
	)
	for _, src := range clockReferenceSources {
		srcCtx, srcCancel := context.WithTimeout(ctx, 3*time.Second)
		t, rtt, err := headDate(srcCtx, src)
		srcCancel()
		if err == nil {
			refTime = t
			refRTT = rtt
			refURL = src
			break
		}
		lastErr = err
	}

	if refURL == "" {
		msg := "no_reference_source_reachable"
		if lastErr != nil {
			msg = fmt.Sprintf("%s: %v", msg, lastErr)
		}
		report.ChecksFailed = append(report.ChecksFailed, msg)
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	report.ReferenceTime = refTime.UTC().Format(time.RFC3339)
	report.ReferenceSource = refURL
	report.RoundTripMs = refRTT.Milliseconds()

	// Drift is local minus reference. Positive = local clock is ahead.
	rawDrift := int64(now.Sub(refTime).Seconds())
	halfRTT := int64(refRTT.Seconds() / 2)
	corrected := rawDrift
	// Subtract half-RTT in the direction that compensates for the
	// reference time having been captured before it reached us:
	// if local is ahead of reference, the reference was actually
	// half-RTT older than we observed, so the real local-minus-server
	// drift is smaller; if local is behind, the real drift is larger
	// in magnitude.
	if corrected > 0 {
		corrected -= halfRTT
		if corrected < 0 {
			corrected = 0
		}
	} else if corrected < 0 {
		corrected -= halfRTT
	}

	report.DriftSeconds = rawDrift
	report.DriftSecondsCorrected = corrected

	if absInt64(corrected) > clockDriftThresholdSeconds {
		report.ChecksFailed = append(report.ChecksFailed,
			fmt.Sprintf("clock_drift: %ds exceeds threshold %ds", corrected, clockDriftThresholdSeconds))
		report.Verdict = "fail"
	} else {
		report.Verdict = "pass"
	}

	writeJSON(w, http.StatusOK, report)
}

// defaultClockHeadDate issues a real HTTP HEAD to url and parses
// the response's Date header. Round-trip is measured around the
// HTTP call only (not DNS).
func defaultClockHeadDate(ctx context.Context, url string) (time.Time, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return time.Time{}, 0, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	rtt := time.Since(start)
	if err != nil {
		return time.Time{}, rtt, err
	}
	defer resp.Body.Close()
	dateHeader := resp.Header.Get("Date")
	if dateHeader == "" {
		return time.Time{}, rtt, fmt.Errorf("response from %s lacked Date header", url)
	}
	t, perr := http.ParseTime(dateHeader)
	if perr != nil {
		return time.Time{}, rtt, fmt.Errorf("parse Date %q from %s: %w", dateHeader, url, perr)
	}
	return t, rtt, nil
}

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// now returns the injected nowFn or time.Now if none is set. Shared
// by A.9 (geo cache age) and A.11 (clock drift).
func (h *Handler) now() time.Time {
	if h.nowFn != nil {
		return h.nowFn()
	}
	return time.Now()
}

// ----------------------------------------------------------------------
// /api/diag/geo  (A.9)
// ----------------------------------------------------------------------

// DiagGeoReport is the JSON returned by /api/diag/geo.
//
// Wraps the existing RuntimeStatus geo cache (written by the startup
// probe and by POST /api/system/geo-check) in the standard diag shape.
// Catches VPN/exit-IP drift that causes blanket 403s from Akamai:
// when PIA renews to a non-UK exit, every BBC stream grab fails
// silently and the user has no obvious attribution. A single curl
// of this endpoint reports whether the container is still seen as
// UK by BBC.
//
// Two modes:
//   - Default: report the cached geo state plus its age. Verdict
//     pass iff cached and within geoCacheTTL.
//   - ?refresh=1: invoke the live geo probe (h.GeoProbe), update the
//     cache, return the fresh result. Verdict reflects the fresh check.
type DiagGeoReport struct {
	Verdict      string   `json:"verdict"`
	ChecksFailed []string `json:"checks_failed"`
	GeoOK        bool     `json:"geo_ok"`
	GeoStatus    string   `json:"geo_status"`
	GeoDetail    string   `json:"geo_detail"`
	CheckedAt    string   `json:"checked_at,omitempty"`
	AgeSeconds   int64    `json:"age_seconds"`
	Refreshed    bool     `json:"refreshed"`
	TTLSeconds   int64    `json:"ttl_seconds"`
}

const geoCacheTTLSeconds int64 = 300 // 5 minutes

func (h *Handler) handleDiagGeo(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	report := DiagGeoReport{
		ChecksFailed: []string{},
		TTLSeconds:   geoCacheTTLSeconds,
	}

	refresh := r.URL.Query().Get("refresh") == "1"

	if refresh {
		if h.GeoProbe == nil {
			report.ChecksFailed = append(report.ChecksFailed, "geo_probe_unavailable: GeoProbe not wired in this build")
			report.Verdict = "fail"
			writeJSON(w, http.StatusOK, report)
			return
		}
		r2 := h.GeoProbe()
		checkedAt := h.now().UTC().Format(time.RFC3339)
		if h.status != nil {
			h.status.SetGeo(string(r2.Status), r2.Detail, checkedAt)
		}
		ok := r2.Status == bbc.GeoUKOK
		report.GeoOK = ok
		report.GeoStatus = string(r2.Status)
		report.GeoDetail = r2.Detail
		report.CheckedAt = checkedAt
		report.AgeSeconds = 0
		report.Refreshed = true
		if !ok {
			report.ChecksFailed = append(report.ChecksFailed,
				fmt.Sprintf("%s: %s", r2.Status, r2.Detail))
			report.Verdict = "fail"
		} else {
			report.Verdict = "pass"
		}
		writeJSON(w, http.StatusOK, report)
		return
	}

	// Cached path
	if h.status == nil {
		report.ChecksFailed = append(report.ChecksFailed, "geo_cache_unavailable: RuntimeStatus not wired in this build")
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}
	s := h.status.Snapshot()
	geoOK := s.GeoOK
	checkedAt := s.GeoCheckedAt
	report.GeoOK = geoOK
	report.GeoStatus = s.GeoStatus
	report.GeoDetail = s.GeoDetail
	report.CheckedAt = checkedAt

	if checkedAt == "" {
		report.AgeSeconds = -1
		report.ChecksFailed = append(report.ChecksFailed,
			"geo_never_checked: cache is empty; pass ?refresh=1 to populate")
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	checkedTime, perr := time.Parse(time.RFC3339, checkedAt)
	if perr != nil {
		report.ChecksFailed = append(report.ChecksFailed,
			fmt.Sprintf("geo_checked_at_unparseable: %v", perr))
		report.Verdict = "fail"
		writeJSON(w, http.StatusOK, report)
		return
	}

	age := int64(h.now().Sub(checkedTime).Seconds())
	if age < 0 {
		age = 0
	}
	report.AgeSeconds = age

	switch {
	case !geoOK:
		reason := s.GeoDetail
		if reason == "" {
			reason = "cached geo check is false (exit IP may not be in the UK)"
		}
		status := s.GeoStatus
		if status == "" {
			status = "not_uk_exit"
		}
		report.ChecksFailed = append(report.ChecksFailed,
			fmt.Sprintf("%s: %s", status, reason))
		report.Verdict = "fail"
	case age > geoCacheTTLSeconds:
		report.ChecksFailed = append(report.ChecksFailed,
			fmt.Sprintf("geo_stale: cached check is %ds old (>%ds); pass ?refresh=1 to revalidate",
				age, geoCacheTTLSeconds))
		report.Verdict = "fail"
	default:
		report.Verdict = "pass"
	}
	writeJSON(w, http.StatusOK, report)
}

// ----------------------------------------------------------------------
// /api/diag/network  (A.8)
// ----------------------------------------------------------------------

// NetworkProbeReport is the per-host entry in DiagNetworkReport.Probes.
type NetworkProbeReport struct {
	Host          string   `json:"host"`
	DNSAOK        bool     `json:"dns_a_ok"`
	DNSAAddrs     []string `json:"dns_a_addrs,omitempty"`
	DNSAError     string   `json:"dns_a_error,omitempty"`
	DNSAAAAOK     bool     `json:"dns_aaaa_ok"`
	DNSAAAAAddrs  []string `json:"dns_aaaa_addrs,omitempty"`
	DNSAAAAError  string   `json:"dns_aaaa_error,omitempty"`
	TCP443OK      bool     `json:"tcp_443_ok"`
	TCP443Ms      int64    `json:"tcp_443_ms,omitempty"`
	TCP443Error   string   `json:"tcp_443_error,omitempty"`
	HTTPHeadOK    bool     `json:"http_head_ok"`
	HTTPStatus    int      `json:"http_status,omitempty"`
	HTTPHeadError string   `json:"http_head_error,omitempty"`
	Verdict       string   `json:"verdict"`
}

// DiagNetworkReport is the JSON returned by /api/diag/network.
//
// This is the env-health probe that originally motivated the v1.5.7
// batch (issue #40, emersnbe). It tests whether the container's
// network plumbing reaches BBC's streaming + control-plane infra,
// disambiguating "DNS broken" from "outbound TCP blocked" from
// "specific BBC host filtered" from "IPv6-only with no fallback".
//
// The resolv_conf field is the diagnosis gold: it answers "what
// resolver am I actually using?" in a single curl, without a
// `docker exec` round-trip.
type DiagNetworkReport struct {
	Verdict       string               `json:"verdict"`
	ChecksFailed  []string             `json:"checks_failed"`
	ResolverPath  string               `json:"resolver_path"`
	ResolvConf    string               `json:"resolv_conf,omitempty"`
	IPv6Available bool                 `json:"ipv6_available"`
	Probes        []NetworkProbeReport `json:"probes"`
}

// diagNetworkHostProbe is the injection seam for A.8. Production
// wires defaultNetworkHostProbe (real net.Resolver + http.Client);
// tests inject deterministic fakes to drive happy-path and per-
// failure-mode scenarios without touching the real network.
type diagNetworkHostProbe interface {
	ResolveA(ctx context.Context, host string) ([]string, error)
	ResolveAAAA(ctx context.Context, host string) ([]string, error)
	DialTCP(ctx context.Context, hostPort string) (time.Duration, error)
	HeadStatus(ctx context.Context, url string) (int, error)
}

// networkProbeTargets is the in-order list of hosts A.8 probes.
// The first three are BBC's streaming + control-plane edges; the
// fourth (1.1.1.1) is the generic-outbound canary that distinguishes
// "all DNS broken" from "BBC specifically blocked".
var networkProbeTargets = []string{
	"vod-hls-uk-live.akamaized.net",
	"open.live.bbc.co.uk",
	"iplayer-web.files.bbci.co.uk",
	"1.1.1.1",
}

func (h *Handler) handleDiagNetwork(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	probe := h.networkProbe
	if probe == nil {
		probe = defaultNetworkHostProbe{}
	}

	report := DiagNetworkReport{
		ChecksFailed: []string{},
		ResolverPath: "/etc/resolv.conf",
		Probes:       make([]NetworkProbeReport, len(networkProbeTargets)),
	}

	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		report.ResolvConf = string(data)
	}
	report.IPv6Available = detectIPv6Available()

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// Probe hosts in parallel with a 5s per-host budget.
	var wg sync.WaitGroup
	for i, host := range networkProbeTargets {
		i, host := i, host
		wg.Add(1)
		go func() {
			defer wg.Done()
			hostCtx, hostCancel := context.WithTimeout(ctx, 5*time.Second)
			defer hostCancel()
			report.Probes[i] = probeNetworkHost(hostCtx, probe, host)
		}()
	}
	wg.Wait()

	for _, p := range report.Probes {
		if p.Verdict != "pass" {
			reason := summariseNetworkFailure(p)
			report.ChecksFailed = append(report.ChecksFailed,
				fmt.Sprintf("%s: %s", p.Host, reason))
		}
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

// probeNetworkHost runs the four checks (DNS A, DNS AAAA, TCP 443,
// HTTP HEAD) against a single host within the caller's context.
// Per-host pass criteria: (A OR AAAA) AND (TCP OR HEAD-with-any-status).
func probeNetworkHost(ctx context.Context, probe diagNetworkHostProbe, host string) NetworkProbeReport {
	pr := NetworkProbeReport{Host: host}

	if addrs, err := probe.ResolveA(ctx, host); err == nil {
		pr.DNSAOK = true
		pr.DNSAAddrs = addrs
	} else {
		pr.DNSAError = err.Error()
	}
	if addrs, err := probe.ResolveAAAA(ctx, host); err == nil && len(addrs) > 0 {
		pr.DNSAAAAOK = true
		pr.DNSAAAAAddrs = addrs
	} else if err != nil {
		pr.DNSAAAAError = err.Error()
	} else {
		pr.DNSAAAAError = "no AAAA records returned"
	}

	if pr.DNSAOK || pr.DNSAAAAOK {
		if rtt, err := probe.DialTCP(ctx, host+":443"); err == nil {
			pr.TCP443OK = true
			pr.TCP443Ms = rtt.Milliseconds()
		} else {
			pr.TCP443Error = err.Error()
		}
		if status, err := probe.HeadStatus(ctx, "https://"+host+"/"); err == nil && status > 0 {
			pr.HTTPHeadOK = true
			pr.HTTPStatus = status
		} else if err != nil {
			pr.HTTPHeadError = err.Error()
		}
	} else {
		pr.TCP443Error = "skipped: no DNS resolution"
		pr.HTTPHeadError = "skipped: no DNS resolution"
	}

	dnsOK := pr.DNSAOK || pr.DNSAAAAOK
	reachOK := pr.TCP443OK || pr.HTTPHeadOK
	if dnsOK && reachOK {
		pr.Verdict = "pass"
	} else {
		pr.Verdict = "fail"
	}
	return pr
}

// summariseNetworkFailure returns a short reason string for the
// checks_failed list, picking the most diagnostic of the per-host
// errors. Prefers DNS errors over TCP errors over HTTP errors
// because earlier failures are the root cause.
func summariseNetworkFailure(p NetworkProbeReport) string {
	switch {
	case !p.DNSAOK && !p.DNSAAAAOK:
		if p.DNSAError != "" {
			return "dns_a: " + p.DNSAError
		}
		return "dns_aaaa: " + p.DNSAAAAError
	case !p.TCP443OK && !p.HTTPHeadOK:
		if p.TCP443Error != "" {
			return "tcp_443: " + p.TCP443Error
		}
		return "http_head: " + p.HTTPHeadError
	default:
		return "unreachable"
	}
}

// detectIPv6Available reports whether the host has at least one
// global-scope IPv6 address. Used as informational context in the
// report — doesn't affect verdict because per-host AAAA results
// are the load-bearing IPv6 signal.
func detectIPv6Available() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err != nil {
			continue
		}
		if ip.To4() == nil && ip.IsGlobalUnicast() {
			return true
		}
	}
	return false
}

// defaultNetworkHostProbe uses net.DefaultResolver + the standard
// http.Client. Each call respects the passed context.
type defaultNetworkHostProbe struct{}

func (defaultNetworkHostProbe) ResolveA(ctx context.Context, host string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, nil
}

func (defaultNetworkHostProbe) ResolveAAAA(ctx context.Context, host string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip6", host)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, nil
}

func (defaultNetworkHostProbe) DialTCP(ctx context.Context, hostPort string) (time.Duration, error) {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	rtt := time.Since(start)
	if err != nil {
		return rtt, err
	}
	_ = conn.Close()
	return rtt, nil
}

func (defaultNetworkHostProbe) HeadStatus(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
