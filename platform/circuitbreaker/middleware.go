package circuitbreaker

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
)

type errorBody struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

// NewMiddleware returns an optional HTTP/Chi middleware for route-level circuit breaking.
// If the circuit is open, it halts the request chain immediately and responds with HTTP 503.
// If admitted, it stores the Permit in the request context so any downstream RoundTripper
// can reuse it without double-admission.
func NewMiddleware(breaker *Breaker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if breaker == nil {
				next.ServeHTTP(w, r)
				return
			}

			permit, err := breaker.Allow()
			if err != nil {
				writeCircuitOpenResponse(w, breaker)
				return
			}

			// Store permit in context so RoundTripper does not perform a second Allow() call
			ctx := WithPermit(r.Context(), permit)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeCircuitOpenResponse(w http.ResponseWriter, b *Breaker) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Circuit-Breaker", "OPEN")

	// Calculate Retry-After dynamically
	remaining := b.RemainingCooldown()
	if b.State() == StateOpen {
		seconds := int(math.Ceil(remaining.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	} else if b.State() == StateHalfOpen {
		// When Half-Open trial capacity is exhausted, duration is indeterminate.
		// Provide an advisory 1s retry hint.
		w.Header().Set("Retry-After", "1")
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(errorBody{
		Success: false,
		Error:   "CIRCUIT_BREAKER_OPEN",
		Message: "The upstream service is temporarily unavailable. Please retry after the cooldown period.",
	})
}
