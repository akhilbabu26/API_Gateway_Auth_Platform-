package circuitbreaker

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoundTripperDedicated(t *testing.T) {
	var upstreamCalls int64
	var shouldFail int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&upstreamCalls, 1)
		if atomic.LoadInt32(&shouldFail) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 2,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	client := &http.Client{
		Transport: NewRoundTripper(breaker, nil, server.Client().Transport),
	}

	// 1. Success call
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	if breaker.State() != StateClosed {
		t.Fatalf("breaker state = %v, want CLOSED", breaker.State())
	}

	// 2. Upstream begins failing
	atomic.StoreInt32(&shouldFail, 1)

	// Failure 1
	resp, err = client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if breaker.State() != StateClosed || breaker.ConsecutiveFailures() != 1 {
		t.Fatalf("after 1 fail: state = %v, failures = %d", breaker.State(), breaker.ConsecutiveFailures())
	}

	// Failure 2 -> Trips to OPEN!
	resp, err = client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if breaker.State() != StateOpen {
		t.Fatalf("after 2 fails: state = %v, want OPEN", breaker.State())
	}

	callsBefore := atomic.LoadInt64(&upstreamCalls)

	// 3. Subsequent calls rejected immediately by RoundTripper with ErrCircuitOpen
	_, err = client.Get(server.URL)
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	// Verify upstream was NOT invoked
	if atomic.LoadInt64(&upstreamCalls) != callsBefore {
		t.Fatalf("upstream was called while circuit was OPEN!")
	}
}

func TestRoundTripperClientCancellationAbandons(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Trip to OPEN
	p, _ := breaker.Allow()
	p.Failure()

	// Advance to HALF-OPEN
	clock.Advance(10 * time.Second)

	client := &http.Client{
		Transport: NewRoundTripper(breaker, nil, server.Client().Transport),
	}

	// Send request with an immediately canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)

	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected client error on canceled request")
	}

	// The canceled request must have triggered Abandon(), keeping breaker in HALF-OPEN
	// rather than tripping back to OPEN!
	if breaker.State() != StateHalfOpen {
		t.Fatalf("breaker state after cancellation = %v, want HALF-OPEN", breaker.State())
	}
	if breaker.HalfOpenInFlight() != 0 {
		t.Fatalf("halfOpenInFlight = %d, want 0", breaker.HalfOpenInFlight())
	}
}

func TestContextDeduplicationBetweenMiddlewareAndRoundTripper(t *testing.T) {
	var upstreamCalls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&upstreamCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 2,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Wrap handler with Middleware
	mw := NewMiddleware(breaker)
	rt := NewRoundTripper(breaker, nil, server.Client().Transport)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler forwards request using the RoundTripper
		outReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, server.URL, nil)
		resp, err := rt.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("response status = %d, want 200", rec.Code)
	}
	if atomic.LoadInt64(&upstreamCalls) != 1 {
		t.Fatalf("upstreamCalls = %d, want 1", atomic.LoadInt64(&upstreamCalls))
	}
}

func TestMiddlewareRejectionResponse(t *testing.T) {
	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 1,
		Cooldown:         25 * time.Second,
		HalfOpenMaxCalls: 1,
		Clock:            clock,
	})

	// Trip breaker
	p, _ := breaker.Allow()
	p.Failure()

	clock.Advance(5 * time.Second) // 20s remaining

	mw := NewMiddleware(breaker)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "20" {
		t.Errorf("Retry-After = %q, want 20", got)
	}
	if got := rec.Header().Get("X-Circuit-Breaker"); got != "OPEN" {
		t.Errorf("X-Circuit-Breaker = %q, want OPEN", got)
	}
	if !strings.Contains(rec.Body.String(), `"error":"CIRCUIT_BREAKER_OPEN"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestSharedRoundTripper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Should-Fail") == "true" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := NewManager(Config{
		FailureThreshold: 2,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 1,
	})

	sharedRT := NewSharedRoundTripper(m, nil, server.Client().Transport)
	client := &http.Client{Transport: sharedRT}

	// 1. Send 2 failures to "payments" upstream
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		req.Header.Set("X-Should-Fail", "true")
		ctx := WithUpstreamID(req.Context(), "payments")
		resp, err := client.Do(req.WithContext(ctx))
		if err != nil {
			t.Fatalf("payments request %d failed: %v", i, err)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
	}

	paymentsBreaker, _ := m.Get("payments")
	if paymentsBreaker.State() != StateOpen {
		t.Fatalf("payments breaker state = %v, want OPEN", paymentsBreaker.State())
	}

	// 2. Payments request is now rejected
	reqPayments, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	ctxPayments := WithUpstreamID(reqPayments.Context(), "payments")
	_, err := client.Do(reqPayments.WithContext(ctxPayments))
	if !stderrors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen for payments, got %v", err)
	}

	// 3. Orders request succeeds normally
	reqOrders, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	ctxOrders := WithUpstreamID(reqOrders.Context(), "orders")
	resp, err := client.Do(reqOrders.WithContext(ctxOrders))
	if err != nil {
		t.Fatalf("orders request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orders status = %d, want 200", resp.StatusCode)
	}
}
