package circuitbreaker

import "sync/atomic"

// Outcome represents the execution outcome of an admitted upstream attempt.
type Outcome uint8

const (
	// OutcomeSuccess indicates the upstream request completed successfully.
	OutcomeSuccess Outcome = iota
	// OutcomeFailure indicates an upstream system or network failure.
	OutcomeFailure
	// OutcomeAbandon indicates execution was aborted or canceled by the client
	// without upstream fault, freeing trial capacity.
	OutcomeAbandon
)

// Permit is a single-use token issued upon request admission.
type Permit struct {
	breaker    *Breaker
	generation uint64
	state      State
	done       uint32 // Atomic flag: 0 = active, 1 = completed
}

// Success reports that the upstream request succeeded.
// In CLOSED state, this resets the consecutive failure counter.
// In HALF-OPEN state, this closes the circuit back to normal operation.
func (p *Permit) Success() {
	if p == nil || !atomic.CompareAndSwapUint32(&p.done, 0, 1) {
		return
	}
	p.breaker.report(p.generation, OutcomeSuccess)
}

// Failure reports that the upstream request encountered a server or transport failure.
// In CLOSED state, this increments the consecutive failure counter and trips if threshold is reached.
// In HALF-OPEN state, this immediately reopens the circuit with a fresh cooldown.
func (p *Permit) Failure() {
	if p == nil || !atomic.CompareAndSwapUint32(&p.done, 0, 1) {
		return
	}
	p.breaker.report(p.generation, OutcomeFailure)
}

// Abandon reports that the request was aborted (e.g. client cancellation or client timeout)
// without blaming the upstream service.
// In HALF-OPEN state, this releases the trial slot without recording a failure.
// In CLOSED state, this is a no-op.
func (p *Permit) Abandon() {
	if p == nil || !atomic.CompareAndSwapUint32(&p.done, 0, 1) {
		return
	}
	p.breaker.report(p.generation, OutcomeAbandon)
}

// State returns the breaker state when the permit was issued.
func (p *Permit) State() State {
	if p == nil {
		return StateClosed
	}
	return p.state
}

// Generation returns the breaker generation when the permit was issued.
func (p *Permit) Generation() uint64 {
	if p == nil {
		return 0
	}
	return p.generation
}
