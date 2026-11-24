package circuitbreaker

import (
	stderrors "errors"
	"sync"
	"testing"
	"time"
)

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(initial time.Time) *mockClock {
	return &mockClock{now: initial}
}

func (m *mockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *mockClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "CLOSED"},
		{StateOpen, "OPEN"},
		{StateHalfOpen, "HALF-OPEN"},
		{State(99), "UNKNOWN(99)"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		cfg := Config{
			FailureThreshold: 3,
			Cooldown:         10 * time.Second,
			HalfOpenMaxCalls: 1,
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})

	t.Run("invalid FailureThreshold", func(t *testing.T) {
		cfg := Config{
			FailureThreshold: 0,
			Cooldown:         10 * time.Second,
			HalfOpenMaxCalls: 1,
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for FailureThreshold < 1")
		}
	})

	t.Run("invalid Cooldown", func(t *testing.T) {
		cfg := Config{
			FailureThreshold: 3,
			Cooldown:         0,
			HalfOpenMaxCalls: 1,
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for Cooldown <= 0")
		}
	})

	t.Run("invalid HalfOpenMaxCalls", func(t *testing.T) {
		cfg := Config{
			FailureThreshold: 3,
			Cooldown:         10 * time.Second,
			HalfOpenMaxCalls: 0,
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for HalfOpenMaxCalls < 1")
		}
	})

	t.Run("WithDefaults applies fallback", func(t *testing.T) {
		cfg := Config{}.WithDefaults()
		if cfg.FailureThreshold != 5 {
			t.Errorf("default FailureThreshold = %d, want 5", cfg.FailureThreshold)
		}
		if cfg.Cooldown != 30*time.Second {
			t.Errorf("default Cooldown = %v, want 30s", cfg.Cooldown)
		}
		if cfg.HalfOpenMaxCalls != 2 {
			t.Errorf("default HalfOpenMaxCalls = %d, want 2", cfg.HalfOpenMaxCalls)
		}
		if cfg.Clock == nil {
			t.Error("default Clock should not be nil")
		}
	})
}

func TestInitialClosedStateAndSuccess(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 3,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	if b.State() != StateClosed {
		t.Fatalf("initial state = %v, want CLOSED", b.State())
	}

	permit, err := b.Allow()
	if err != nil {
		t.Fatalf("Allow() error = %v, want nil", err)
	}
	if permit == nil {
		t.Fatal("permit is nil")
	}
	if permit.State() != StateClosed {
		t.Errorf("permit state = %v, want CLOSED", permit.State())
	}

	permit.Success()
	if b.ConsecutiveFailures() != 0 {
		t.Errorf("consecutive failures = %d, want 0", b.ConsecutiveFailures())
	}
}

func TestConsecutiveFailuresAndTrippingToOpen(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 3,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// First failure
	p1, _ := b.Allow()
	p1.Failure()
	if b.ConsecutiveFailures() != 1 || b.State() != StateClosed {
		t.Fatalf("after 1 failure: count = %d, state = %v", b.ConsecutiveFailures(), b.State())
	}

	// Success resets count
	p2, _ := b.Allow()
	p2.Success()
	if b.ConsecutiveFailures() != 0 || b.State() != StateClosed {
		t.Fatalf("after success: count = %d, state = %v", b.ConsecutiveFailures(), b.State())
	}

	// Two consecutive failures
	p3, _ := b.Allow()
	p3.Failure()
	p4, _ := b.Allow()
	p4.Failure()
	if b.ConsecutiveFailures() != 2 || b.State() != StateClosed {
		t.Fatalf("after 2 failures: count = %d, state = %v", b.ConsecutiveFailures(), b.State())
	}

	// Third consecutive failure -> Trips to OPEN!
	genBefore := b.Generation()
	p5, _ := b.Allow()
	p5.Failure()

	if b.State() != StateOpen {
		t.Fatalf("after 3 failures: state = %v, want OPEN", b.State())
	}
	if b.ConsecutiveFailures() != 0 {
		t.Errorf("failure count should reset on trip, got %d", b.ConsecutiveFailures())
	}
	if b.Generation() <= genBefore {
		t.Errorf("generation did not advance: before = %d, now = %d", genBefore, b.Generation())
	}
}

func TestOpenStateFastRejectionAndRemainingCooldown(t *testing.T) {
	startTime := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clock := newMockClock(startTime)
	b := New(Config{
		FailureThreshold: 2,
		Cooldown:         20 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Trip to OPEN
	p1, _ := b.Allow()
	p1.Failure()
	p2, _ := b.Allow()
	p2.Failure()

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want OPEN", b.State())
	}

	// Immediate rejection
	permit, err := b.Allow()
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow() error = %v, want ErrCircuitOpen", err)
	}
	if permit != nil {
		t.Fatal("permit should be nil when rejected")
	}

	// Check remaining cooldown at T+5s
	clock.Advance(5 * time.Second)
	rem := b.RemainingCooldown()
	if rem != 15*time.Second {
		t.Errorf("RemainingCooldown() = %v, want 15s", rem)
	}

	// Still rejected at T+19s
	clock.Advance(14 * time.Second)
	_, err = b.Allow()
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow() at 19s: err = %v, want ErrCircuitOpen", err)
	}
}

func TestCooldownExpirationTransitionsToHalfOpen(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 2,
		Clock:            clock,
	})

	// Trip to OPEN
	p, _ := b.Allow()
	p.Failure()
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want OPEN", b.State())
	}

	// Advance clock past cooldown
	clock.Advance(10 * time.Second)

	// Next Allow() should transition to HALF-OPEN and admit first trial request
	pTrial1, err := b.Allow()
	if err != nil {
		t.Fatalf("Allow() trial 1 err = %v, want nil", err)
	}
	if pTrial1.State() != StateHalfOpen {
		t.Errorf("trial 1 permit state = %v, want HALF-OPEN", pTrial1.State())
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("breaker state = %v, want HALF-OPEN", b.State())
	}
	if b.HalfOpenInFlight() != 1 {
		t.Errorf("halfOpenInFlight = %d, want 1", b.HalfOpenInFlight())
	}

	// Second trial call admitted
	pTrial2, err := b.Allow()
	if err != nil {
		t.Fatalf("Allow() trial 2 err = %v, want nil", err)
	}
	if b.HalfOpenInFlight() != 2 {
		t.Errorf("halfOpenInFlight = %d, want 2", b.HalfOpenInFlight())
	}

	// Third call exceeds HalfOpenMaxCalls (2) -> rejected!
	_, err = b.Allow()
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow() trial 3 err = %v, want ErrCircuitOpen", err)
	}

	// Complete trial 1 with success -> Closes circuit!
	pTrial1.Success()
	if b.State() != StateClosed {
		t.Fatalf("after trial success: state = %v, want CLOSED", b.State())
	}
	if b.HalfOpenInFlight() != 0 {
		t.Errorf("halfOpenInFlight = %d, want 0", b.HalfOpenInFlight())
	}

	// Slower trial 2 finishes with failure, but belongs to old generation -> discarded!
	pTrial2.Failure()
	if b.State() != StateClosed {
		t.Fatalf("stale trial failure corrupted state! state = %v, want CLOSED", b.State())
	}
}

func TestHalfOpenFailureReopensCircuit(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Trip to OPEN
	p, _ := b.Allow()
	p.Failure()

	// Advance past cooldown
	clock.Advance(10 * time.Second)

	// Admitted trial
	trial, err := b.Allow()
	if err != nil {
		t.Fatalf("trial Allow() err = %v", err)
	}

	// Trial fails -> immediately trips to OPEN with fresh cooldown
	trial.Failure()
	if b.State() != StateOpen {
		t.Fatalf("state after trial failure = %v, want OPEN", b.State())
	}

	// Cooldown was reset
	if b.RemainingCooldown() != 10*time.Second {
		t.Errorf("RemainingCooldown() = %v, want 10s", b.RemainingCooldown())
	}

	// Subsequent calls rejected
	_, err = b.Allow()
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow() after reopen err = %v, want ErrCircuitOpen", err)
	}
}

func TestHalfOpenAbandonReleasesTrialCapacity(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Trip to OPEN
	p, _ := b.Allow()
	p.Failure()

	clock.Advance(10 * time.Second)

	// Admitted trial
	trial1, err := b.Allow()
	if err != nil {
		t.Fatalf("trial 1 Allow() err = %v", err)
	}
	if b.HalfOpenInFlight() != 1 {
		t.Errorf("halfOpenInFlight = %d, want 1", b.HalfOpenInFlight())
	}

	// Another call is rejected because slot is full
	_, err = b.Allow()
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen when trial slot is occupied")
	}

	// Client cancels or disconnects -> Abandon()
	trial1.Abandon()

	// Capacity released, state remains HALF-OPEN, NOT OPEN!
	if b.State() != StateHalfOpen {
		t.Fatalf("state after abandon = %v, want HALF-OPEN", b.State())
	}
	if b.HalfOpenInFlight() != 0 {
		t.Errorf("halfOpenInFlight after abandon = %d, want 0", b.HalfOpenInFlight())
	}

	// A new trial call can now be admitted!
	trial2, err := b.Allow()
	if err != nil {
		t.Fatalf("trial 2 Allow() err = %v, want nil", err)
	}
	trial2.Success()
	if b.State() != StateClosed {
		t.Fatalf("state after trial 2 success = %v, want CLOSED", b.State())
	}
}

func TestPermitDuplicateCompletionIsIdempotent(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 2,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	p, _ := b.Allow()

	// First call records failure
	p.Failure()
	if b.ConsecutiveFailures() != 1 {
		t.Fatalf("consecutive failures = %d, want 1", b.ConsecutiveFailures())
	}

	// Duplicate calls have zero effect
	p.Failure()
	p.Success()
	p.Abandon()

	if b.ConsecutiveFailures() != 1 {
		t.Errorf("duplicate calls corrupted failure count: %d, want 1", b.ConsecutiveFailures())
	}
}

func TestClosedStateAbandonIsNoOp(t *testing.T) {
	clock := newMockClock(time.Now())
	b := New(Config{
		FailureThreshold: 3,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	p1, _ := b.Allow()
	p1.Failure()
	if b.ConsecutiveFailures() != 1 {
		t.Fatalf("failures = %d, want 1", b.ConsecutiveFailures())
	}

	p2, _ := b.Allow()
	p2.Abandon() // Should not change failures

	if b.ConsecutiveFailures() != 1 {
		t.Errorf("abandon changed failures in closed state: %d, want 1", b.ConsecutiveFailures())
	}
	if b.State() != StateClosed {
		t.Errorf("state = %v, want CLOSED", b.State())
	}
}
