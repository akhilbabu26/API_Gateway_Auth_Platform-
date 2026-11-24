package circuitbreaker

import (
	"context"
	"net/http"
)

type contextKey struct {
	name string
}

var (
	permitCtxKey     = &contextKey{name: "circuitbreaker_permit"}
	upstreamIDCtxKey = &contextKey{name: "circuitbreaker_upstream_id"}
)

// WithPermit returns a new context containing the given Permit.
func WithPermit(ctx context.Context, permit *Permit) context.Context {
	return context.WithValue(ctx, permitCtxKey, permit)
}

// PermitFromContext extracts the Permit from the context if present.
func PermitFromContext(ctx context.Context) (*Permit, bool) {
	if ctx == nil {
		return nil, false
	}
	p, ok := ctx.Value(permitCtxKey).(*Permit)
	return p, ok
}

// WithUpstreamID returns a new context annotated with the target logical upstream ID.
func WithUpstreamID(ctx context.Context, upstreamID string) context.Context {
	return context.WithValue(ctx, upstreamIDCtxKey, upstreamID)
}

// UpstreamIDFromContext extracts the logical upstream ID from the context if present.
func UpstreamIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(upstreamIDCtxKey).(string)
	return id, ok
}

// RoundTripper is the primary Circuit Breaker integration layer, wrapping an http.RoundTripper
// (such as ReverseProxy.Transport) to manage upstream admission and failure recording per attempt.
type RoundTripper struct {
	Next       http.RoundTripper
	Breaker    *Breaker
	Classifier Classifier
}

// NewRoundTripper creates a new RoundTripper protecting an upstream with the provided breaker.
func NewRoundTripper(b *Breaker, c Classifier, next http.RoundTripper) *RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if c == nil {
		c = DefaultClassifier{}
	}
	return &RoundTripper{
		Next:       next,
		Breaker:    b,
		Classifier: c,
	}
}

// RoundTrip executes a single HTTP request through the circuit breaker.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. Context Deduplication Check:
	// If outer middleware already admitted this request, reuse the existing permit
	permit, ok := PermitFromContext(req.Context())
	if !ok && rt.Breaker != nil {
		// Primary flow: RoundTripper performs the admission check directly
		var err error
		permit, err = rt.Breaker.Allow()
		if err != nil {
			return nil, err // ErrCircuitOpen
		}
	}

	// 2. Execute the actual HTTP round-trip without holding any locks
	resp, err := rt.Next.RoundTrip(req)

	// 3. Classify outcome and complete permit
	if permit != nil {
		switch rt.Classifier.Classify(req, resp, err) {
		case DecisionSuccess:
			permit.Success()
		case DecisionFailure:
			permit.Failure()
		case DecisionAbandon:
			permit.Abandon()
		}
	}

	return resp, err
}

// SharedRoundTripper routes requests dynamically to per-upstream breakers using the upstream ID
// stored in the request context.
type SharedRoundTripper struct {
	Next       http.RoundTripper
	Manager    *Manager
	Classifier Classifier
}

// NewSharedRoundTripper creates a shared RoundTripper for multi-tenant / multi-route gateways.
func NewSharedRoundTripper(m *Manager, c Classifier, next http.RoundTripper) *SharedRoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if c == nil {
		c = DefaultClassifier{}
	}
	return &SharedRoundTripper{
		Next:       next,
		Manager:    m,
		Classifier: c,
	}
}

// RoundTrip resolves the appropriate breaker from the request context and executes the round trip.
func (srt *SharedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var permit *Permit

	if upstreamID, ok := UpstreamIDFromContext(req.Context()); ok && srt.Manager != nil {
		breaker, err := srt.Manager.GetOrCreate(upstreamID)
		if err != nil {
			return nil, err
		}

		// Check context deduplication first
		if p, found := PermitFromContext(req.Context()); found {
			permit = p
		} else {
			permit, err = breaker.Allow()
			if err != nil {
				return nil, err
			}
		}
	}

	resp, err := srt.Next.RoundTrip(req)

	if permit != nil {
		switch srt.Classifier.Classify(req, resp, err) {
		case DecisionSuccess:
			permit.Success()
		case DecisionFailure:
			permit.Failure()
		case DecisionAbandon:
			permit.Abandon()
		}
	}

	return resp, err
}
