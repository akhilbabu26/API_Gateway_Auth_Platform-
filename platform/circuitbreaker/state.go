package circuitbreaker

import "fmt"

// State represents the operational state of a circuit breaker.
type State uint8

const (
	// StateClosed allows all requests through and counts consecutive failures.
	StateClosed State = iota
	// StateOpen fails fast, rejecting all requests immediately.
	StateOpen
	// StateHalfOpen admits a limited number of trial requests to test upstream recovery.
	StateHalfOpen
)

// String returns a human-readable representation of the circuit breaker state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF-OPEN"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", s)
	}
}
