package download

import (
	"sync"
	"sync/atomic"
	"time"
)

// Adaptive throttle: self-healing response to ffmpeg watchdog stalls.
//
// A stall is a symptom of either BBC per-IP throttling of a high-bitrate
// stream or local CPU/IO contention among concurrent ffmpeg remuxes (#42).
// Both worsen under a Sonarr season grab, exactly when several downloads run
// at once. The throttle watches the stall stream and, when a CLUSTER of
// distinct downloads stalls inside a sliding window, responds on two axes:
//
//  1. Coarse: open a cooldown that pauses NEW admissions for a bounded,
//     exponentially-backed-off window so the in-flight set drains and the
//     throttle/contention clears, then resumes automatically.
//  2. Fine: multiplicatively decrease a dynamic activeLimit (>= floor) that
//     caps concurrent downloads via the admission gate, additively recovering
//     one step per clean window (AIMD) so a sustained pressure settles at a
//     stable lower concurrency without operator tuning.
//
// The worker goroutine pool is never resized (Audit item 25); all control is
// at the admission layer (processNext). The dedicated per-download stall
// budget (store.Download.StallCount) is frozen for clustered victims so a
// synchronised burst cannot silently exhaust it and be lost until the next
// Sonarr RSS; the freeze is bounded by maxStallCredits so a genuinely dead
// stream still reaches permanence. GitHub #50 / Gitea #49.

const (
	// maxStalls is a download's dedicated watchdog-stall budget: after this
	// many stalls that actually consume the budget (i.e. not frozen by a
	// cooldown), the download becomes permanent and is handed to Sonarr for
	// a fresh grab. Independent of the shared RetryCount. GitHub #50.
	maxStalls = 3

	// maxStallCredits bounds how many stalls a single download may have
	// frozen (net-zeroed) by cooldowns, so a persistently-throttled or dead
	// stream still terminates to permanence in bounded cycles rather than
	// looping forever. GitHub #50.
	maxStallCredits = 6
)

// AdaptiveThrottleConfig holds the tunables, resolved once at construction
// from store keys + IPLAYER_ARR_ADAPTIVE_* env (see cmd/iplayer-arr/main.go).
type AdaptiveThrottleConfig struct {
	Enabled      bool
	Threshold    int           // distinct stalls within Window to trip a cooldown
	Window       time.Duration // sliding stall-event window
	BaseCooldown time.Duration // first cooldown length
	MaxCooldown  time.Duration // exponential-backoff ceiling
	CooldownUp   time.Duration // clean interval before an additive activeLimit +1
	Floor        int           // minimum activeLimit
}

// DefaultAdaptiveThrottleConfig returns the shipped defaults.
func DefaultAdaptiveThrottleConfig() AdaptiveThrottleConfig {
	return AdaptiveThrottleConfig{
		Enabled:      true,
		Threshold:    2,
		Window:       120 * time.Second,
		BaseCooldown: 180 * time.Second,
		MaxCooldown:  900 * time.Second,
		CooldownUp:   180 * time.Second,
		Floor:        1,
	}
}

type stallEvent struct {
	id string
	at time.Time
}

// throttleSnapshot is a point-in-time view for telemetry (/api/system).
type throttleSnapshot struct {
	ActiveLimit    int
	MaxWorkers     int
	Throttled      bool
	CooldownUntil  time.Time
	StallsInWindow int
}

type adaptiveThrottle struct {
	cfg     AdaptiveThrottleConfig
	ceiling int // == max_workers; activeLimit never exceeds this
	now     func() time.Time

	activeLimit atomic.Int32 // dynamic concurrency cap, init == ceiling

	mu                sync.Mutex
	events            []stallEvent // sliding window of stall events
	coolingUntilNanos int64        // 0 = not cooling; guarded by mu, read lock-free via atomic load helper
	consecutive       int          // consecutive cooldowns, drives exponential backoff
	nextRecoverAt     time.Time    // single-flight gate: only one worker recovers per clean window
}

func newAdaptiveThrottle(cfg AdaptiveThrottleConfig, ceiling int, now func() time.Time) *adaptiveThrottle {
	if now == nil {
		now = time.Now
	}
	if ceiling < 1 {
		ceiling = 1
	}
	if cfg.Floor < 1 {
		cfg.Floor = 1
	}
	if cfg.Floor > ceiling {
		cfg.Floor = ceiling
	}
	if cfg.Threshold < 1 {
		cfg.Threshold = 1
	}
	t := &adaptiveThrottle{cfg: cfg, ceiling: ceiling, now: now}
	t.activeLimit.Store(int32(ceiling))
	t.nextRecoverAt = now().Add(cfg.CooldownUp)
	return t
}

// coolingUntil reads the cooldown deadline lock-free.
func (t *adaptiveThrottle) coolingUntil() time.Time {
	n := atomic.LoadInt64(&t.coolingUntilNanos)
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// inCooldown reports whether new admissions are currently paused. Pure read
// (no state mutation), so it is safe to call from failDownload. A cooldown
// expires simply by the deadline passing; no explicit clear is needed.
func (t *adaptiveThrottle) inCooldown() bool {
	n := atomic.LoadInt64(&t.coolingUntilNanos)
	return n != 0 && t.now().UnixNano() < n
}

// admit reports whether a worker may claim another download now, given the
// current live concurrency. It enforces both the cooldown pause and the
// dynamic activeLimit. When disabled it never blocks.
func (t *adaptiveThrottle) admit(claimedLen int) bool {
	if !t.cfg.Enabled {
		return true
	}
	if t.inCooldown() {
		return false
	}
	return claimedLen < int(t.activeLimit.Load())
}

// recordStall registers a watchdog stall for download id and, if a cluster of
// distinct downloads has stalled within the window, trips a cooldown and
// multiplicatively decreases activeLimit. Must be called BEFORE failDownload
// so the tripping stall itself is seen as in-cooldown and gets frozen.
func (t *adaptiveThrottle) recordStall(id string) {
	if !t.cfg.Enabled {
		return
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, stallEvent{id: id, at: now})
	t.pruneLocked(now)
	if t.inCooldownLocked(now) {
		return // already cooling; do not re-trip or double-halve
	}
	if t.distinctLocked() < t.cfg.Threshold {
		return
	}
	// Trip: exponential-backoff cooldown + multiplicative decrease.
	d := t.cfg.BaseCooldown
	for i := 0; i < t.consecutive; i++ {
		d *= 2
		if d >= t.cfg.MaxCooldown {
			d = t.cfg.MaxCooldown
			break
		}
	}
	atomic.StoreInt64(&t.coolingUntilNanos, now.Add(d).UnixNano())
	t.consecutive++
	cur := int(t.activeLimit.Load())
	next := cur / 2
	if next < t.cfg.Floor {
		next = t.cfg.Floor
	}
	t.activeLimit.Store(int32(next))
	t.nextRecoverAt = now.Add(d).Add(t.cfg.CooldownUp)
}

// recordClean registers a successful (non-stalled) completion, evidence the
// pressure has eased. It resets the exponential-backoff counter so a later,
// unrelated cluster starts from the base cooldown again.
func (t *adaptiveThrottle) recordClean() {
	if !t.cfg.Enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.inCooldownLocked(t.now()) {
		t.consecutive = 0
	}
}

// maybeRecover additively increases activeLimit by one step when a clean
// window has elapsed with no stalls and no active cooldown. Single-flight:
// every worker calls it each tick, but the nextRecoverAt gate under mu means
// only the first past the deadline increments, then pushes the gate forward,
// so N workers cannot apply N increments for one window.
func (t *adaptiveThrottle) maybeRecover() {
	if !t.cfg.Enabled {
		return
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	if t.inCooldownLocked(now) || len(t.events) > 0 {
		return
	}
	if now.Before(t.nextRecoverAt) {
		return
	}
	cur := int(t.activeLimit.Load())
	if cur < t.ceiling {
		t.activeLimit.Store(int32(cur + 1))
	}
	if int(t.activeLimit.Load()) >= t.ceiling {
		t.consecutive = 0
	}
	t.nextRecoverAt = now.Add(t.cfg.CooldownUp)
}

// clearCooldown ends any active cooldown and restores activeLimit to the
// ceiling. Used by Manager.Resume so a manual resume overrides the throttle.
func (t *adaptiveThrottle) clearCooldown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	atomic.StoreInt64(&t.coolingUntilNanos, 0)
	t.activeLimit.Store(int32(t.ceiling))
	t.consecutive = 0
	t.events = nil
	t.nextRecoverAt = t.now().Add(t.cfg.CooldownUp)
}

func (t *adaptiveThrottle) snapshot() throttleSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.pruneLocked(now)
	return throttleSnapshot{
		ActiveLimit:    int(t.activeLimit.Load()),
		MaxWorkers:     t.ceiling,
		Throttled:      t.inCooldownLocked(now) || int(t.activeLimit.Load()) < t.ceiling,
		CooldownUntil:  t.coolingUntil(),
		StallsInWindow: t.distinctLocked(),
	}
}

// --- locked helpers (caller holds mu) ---

func (t *adaptiveThrottle) pruneLocked(now time.Time) {
	cutoff := now.Add(-t.cfg.Window)
	i := 0
	for i < len(t.events) && !t.events[i].at.After(cutoff) {
		i++
	}
	if i > 0 {
		t.events = t.events[i:]
	}
}

func (t *adaptiveThrottle) distinctLocked() int {
	seen := make(map[string]struct{}, len(t.events))
	for _, e := range t.events {
		seen[e.id] = struct{}{}
	}
	return len(seen)
}

func (t *adaptiveThrottle) inCooldownLocked(now time.Time) bool {
	return t.coolingUntilNanos != 0 && now.UnixNano() < t.coolingUntilNanos
}

// stallBackoff returns the retry delay for a stall at the given stall count,
// mirroring the CDN backoff schedule (30s, 90s, ...) but keyed off the
// dedicated StallCount so a stall's delay is not inflated by unrelated
// not-yet-available or CDN retries on the shared RetryCount. GitHub #50.
func stallBackoff(stallCount int) time.Duration {
	backoff := 30 * time.Second
	for i := 1; i < stallCount; i++ {
		backoff *= 3
	}
	return backoff
}
