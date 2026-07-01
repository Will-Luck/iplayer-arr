package download

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func testThrottle(t *testing.T, ceiling int) (*adaptiveThrottle, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	cfg := DefaultAdaptiveThrottleConfig()
	return newAdaptiveThrottle(cfg, ceiling, clk.now), clk
}

func TestAdaptiveThrottle_InitActiveLimitEqualsCeiling(t *testing.T) {
	th, _ := testThrottle(t, 4)
	if got := int(th.activeLimit.Load()); got != 4 {
		t.Fatalf("activeLimit init = %d, want 4", got)
	}
	if !th.admit(3) {
		t.Error("admit(3) with limit 4 should be true")
	}
	if th.admit(4) {
		t.Error("admit(4) with limit 4 should be false")
	}
}

func TestAdaptiveThrottle_SingleStreamDoesNotTrip(t *testing.T) {
	th, _ := testThrottle(t, 4)
	th.recordStall("dl-1")
	th.recordStall("dl-1") // same id repeatedly
	th.recordStall("dl-1")
	if th.inCooldown() {
		t.Error("a single distinct stalling download must not trip a cooldown")
	}
	if got := int(th.activeLimit.Load()); got != 4 {
		t.Errorf("activeLimit = %d, want 4 (no trip)", got)
	}
}

func TestAdaptiveThrottle_ClusterTripsCooldownAndHalves(t *testing.T) {
	th, _ := testThrottle(t, 4)
	th.recordStall("dl-1")
	if th.inCooldown() {
		t.Fatal("one distinct stall should not trip (threshold 2)")
	}
	th.recordStall("dl-2")
	if !th.inCooldown() {
		t.Fatal("two distinct stalls should trip a cooldown")
	}
	if got := int(th.activeLimit.Load()); got != 2 {
		t.Errorf("activeLimit after trip = %d, want 2 (halved from 4)", got)
	}
	// New admissions are gated during the cooldown.
	if th.admit(0) {
		t.Error("admit during cooldown should be false even with zero live")
	}
}

func TestAdaptiveThrottle_CooldownExpiryResumesAdmission(t *testing.T) {
	th, clk := testThrottle(t, 4)
	th.recordStall("a")
	th.recordStall("b")
	if !th.inCooldown() {
		t.Fatal("expected cooldown")
	}
	clk.advance(DefaultAdaptiveThrottleConfig().BaseCooldown + time.Second)
	if th.inCooldown() {
		t.Error("cooldown should have expired")
	}
	// activeLimit is still halved (2); admit up to that.
	if !th.admit(1) {
		t.Error("admit(1) after cooldown with limit 2 should be true")
	}
	if th.admit(2) {
		t.Error("admit(2) after cooldown with limit 2 should be false")
	}
}

func TestAdaptiveThrottle_ExponentialCooldownBackoff(t *testing.T) {
	th, clk := testThrottle(t, 8)
	cfg := DefaultAdaptiveThrottleConfig()

	trip := func() time.Duration {
		th.recordStall("x")
		th.recordStall("y")
		return th.coolingUntil().Sub(clk.now())
	}
	first := trip()
	if first != cfg.BaseCooldown {
		t.Fatalf("first cooldown = %v, want %v", first, cfg.BaseCooldown)
	}
	// Let it expire but stay 'unhealthy' (consecutive not reset), trip again.
	clk.advance(first + time.Second)
	second := trip()
	if second != 2*cfg.BaseCooldown {
		t.Fatalf("second cooldown = %v, want %v (exponential)", second, 2*cfg.BaseCooldown)
	}
}

func TestAdaptiveThrottle_RecoverAdditiveSingleFlight(t *testing.T) {
	th, clk := testThrottle(t, 4)
	th.recordStall("a")
	th.recordStall("b") // limit 4 -> 2, cooldown open
	// Advance well past cooldown AND window so events prune and cooldown ends.
	cfg := DefaultAdaptiveThrottleConfig()
	clk.advance(cfg.BaseCooldown + cfg.CooldownUp + cfg.Window + time.Second)
	// Many workers call maybeRecover in the same instant: only +1 total.
	for i := 0; i < 10; i++ {
		th.maybeRecover()
	}
	if got := int(th.activeLimit.Load()); got != 3 {
		t.Fatalf("activeLimit after one recover window = %d, want 3 (single-flight +1)", got)
	}
	// Next window recovers to the ceiling and no further.
	clk.advance(cfg.CooldownUp + time.Second)
	th.maybeRecover()
	if got := int(th.activeLimit.Load()); got != 4 {
		t.Fatalf("activeLimit after second window = %d, want 4 (ceiling)", got)
	}
	clk.advance(cfg.CooldownUp + time.Second)
	th.maybeRecover()
	if got := int(th.activeLimit.Load()); got != 4 {
		t.Fatalf("activeLimit = %d, want capped at ceiling 4", got)
	}
}

func TestAdaptiveThrottle_RecoverBlockedWhileStallsInWindow(t *testing.T) {
	th, clk := testThrottle(t, 4)
	th.recordStall("a")
	th.recordStall("b") // trip -> limit 2
	cfg := DefaultAdaptiveThrottleConfig()
	clk.advance(cfg.BaseCooldown + time.Second) // cooldown over, but a stall is still in-window
	th.recordStall("c")                         // fresh in-window event
	th.maybeRecover()
	if got := int(th.activeLimit.Load()); got != 2 {
		t.Errorf("activeLimit = %d, want 2 (recovery blocked while stalls in window)", got)
	}
}

func TestAdaptiveThrottle_ClearCooldownRestoresCeiling(t *testing.T) {
	th, _ := testThrottle(t, 4)
	th.recordStall("a")
	th.recordStall("b")
	th.clearCooldown()
	if th.inCooldown() {
		t.Error("clearCooldown should end the cooldown")
	}
	if got := int(th.activeLimit.Load()); got != 4 {
		t.Errorf("activeLimit after clear = %d, want ceiling 4", got)
	}
}

func TestAdaptiveThrottle_Disabled(t *testing.T) {
	clk := newFakeClock()
	cfg := DefaultAdaptiveThrottleConfig()
	cfg.Enabled = false
	th := newAdaptiveThrottle(cfg, 4, clk.now)
	th.recordStall("a")
	th.recordStall("b")
	if th.inCooldown() {
		t.Error("disabled throttle must never trip")
	}
	if !th.admit(100) {
		t.Error("disabled throttle admit must always be true")
	}
}

func TestAdaptiveThrottle_MaxWorkersOneIsNoOp(t *testing.T) {
	th, _ := testThrottle(t, 1) // ceiling==floor==1
	th.recordStall("a")
	th.recordStall("b")
	if got := int(th.activeLimit.Load()); got != 1 {
		t.Errorf("activeLimit = %d, want 1 (floor==ceiling, no reduction possible)", got)
	}
}

func TestStallBackoff(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, 90 * time.Second},
		{3, 270 * time.Second},
	}
	for _, c := range cases {
		if got := stallBackoff(c.count); got != c.want {
			t.Errorf("stallBackoff(%d) = %v, want %v", c.count, got, c.want)
		}
	}
}

// TestAdaptiveThrottle_ConcurrentRace exercises the throttle from many
// goroutines so `go test -race` proves the locking is sound.
func TestAdaptiveThrottle_ConcurrentRace(t *testing.T) {
	th, _ := testThrottle(t, 8)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			th.recordStall(string(rune('a' + n%5)))
			_ = th.admit(n % 8)
			th.maybeRecover()
			th.recordClean()
			_ = th.snapshot()
			_ = th.inCooldown()
		}(i)
	}
	wg.Wait()
}
