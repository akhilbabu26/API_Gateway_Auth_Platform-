package circuitbreaker

import (
	"platform/errors"
	"sync"
	"time"
)

// ErrCircuitOpen indicates that a request was rejected because the circuit breaker is OPEN
// or because trial capacity in HALF-OPEN state is exhausted.
var ErrCircuitOpen = errors.ErrCircuitOpen

// Breaker implements a thread-safe, in-memory circuit breaker.
type Breaker struct {
	mu                  sync.Mutex
	cfg                 Config
	state               State
	generation          uint64
	consecutiveFailures int
	openedAt            time.Time
	halfOpenInFlight    int
	clock               Clock
}

// New creates a new Breaker with the given configuration.
// If invalid parameters are provided, defaults are applied.
func New(cfg Config) *Breaker {
	validCfg := cfg.WithDefaults()
	return &Breaker{
		cfg:   validCfg,
		state: StateClosed,
		clock: validCfg.Clock,
	}
}

// State returns the current state of the circuit breaker.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkCooldown(b.clock.Now())
	return b.state
}

// Generation returns the current state generation counter.
func (b *Breaker) Generation() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generation
}

// ConsecutiveFailures returns the current consecutive failure count.
func (b *Breaker) ConsecutiveFailures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consecutiveFailures
}

// HalfOpenInFlight returns the number of active trial calls in HALF-OPEN state.
func (b *Breaker) HalfOpenInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.halfOpenInFlight
}

// RemainingCooldown returns the remaining time before an OPEN circuit enters HALF-OPEN.
// Returns 0 if the circuit is not in StateOpen or if cooldown has already expired.
func (b *Breaker) RemainingCooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != StateOpen {
		return 0
	}

	elapsed := b.clock.Now().Sub(b.openedAt)
	if elapsed >= b.cfg.Cooldown {
		return 0
	}
	return b.cfg.Cooldown - elapsed
}

// Allow evaluates whether an incoming request can proceed.
// If admitted, it returns a Permit token that must be completed via Success(), Failure(), or Abandon().
// If rejected, it returns errors.ErrCircuitOpen.
func (b *Breaker) Allow() (*Permit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	b.checkCooldown(now)

	switch b.state {
	case StateClosed:
		return &Permit{
			breaker:    b,
			generation: b.generation,
			state:      StateClosed,
		}, nil

	case StateOpen:
		return nil, errors.ErrCircuitOpen

	case StateHalfOpen:
		if b.halfOpenInFlight >= b.cfg.HalfOpenMaxCalls {
			return nil, errors.ErrCircuitOpen
		}
		b.halfOpenInFlight++
		return &Permit{
			breaker:    b,
			generation: b.generation,
			state:      StateHalfOpen,
		}, nil

	default:
		return nil, errors.ErrCircuitOpen
	}
}

// checkCooldown evaluates whether an OPEN circuit has expired its cooldown window.
// Must be called with b.mu held.
func (b *Breaker) checkCooldown(now time.Time) {
	if b.state != StateOpen {
		return
	}

	if now.Sub(b.openedAt) >= b.cfg.Cooldown {
		b.state = StateHalfOpen
		b.generation++
		b.halfOpenInFlight = 0
	}
}

// report processes the outcome of an admitted attempt.
func (b *Breaker) report(generation uint64, outcome Outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// STALE PERMIT CHECK:
	// If the generation of the permit does not match the breaker's current generation,
	// the circuit state transitioned while this request was in flight.
	// We discard the report to prevent stale results from corrupting the new state.
	if generation != b.generation {
		return
	}

	switch b.state {
	case StateClosed:
		switch outcome {
		case OutcomeSuccess:
			b.consecutiveFailures = 0
		case OutcomeFailure:
			b.consecutiveFailures++
			if b.consecutiveFailures >= b.cfg.FailureThreshold {
				b.toOpen(b.clock.Now())
			}
		case OutcomeAbandon:
			// No-op in closed state
		}

	case StateHalfOpen:
		if b.halfOpenInFlight > 0 {
			b.halfOpenInFlight--
		}

		switch outcome {
		case OutcomeSuccess:
			// Trial request succeeded: recover to CLOSED!
			b.toClosed()
		case OutcomeFailure:
			// Any trial failure trips immediately back to OPEN with fresh cooldown!
			b.toOpen(b.clock.Now())
		case OutcomeAbandon:
			// Trial was abandoned (e.g. client cancellation): capacity is freed,
			// state remains HALF-OPEN.
		}

	case StateOpen:
		// Results arriving while OPEN from prior generation are already filtered by generation check.
	}
}

// toOpen transitions the breaker to StateOpen. Must be called with b.mu held.
func (b *Breaker) toOpen(now time.Time) {
	b.state = StateOpen
	b.openedAt = now
	b.generation++
	b.consecutiveFailures = 0
	b.halfOpenInFlight = 0
}

// toClosed transitions the breaker to StateClosed. Must be called with b.mu held.
func (b *Breaker) toClosed() {
	b.state = StateClosed
	b.generation++
	b.consecutiveFailures = 0
	b.halfOpenInFlight = 0
}
