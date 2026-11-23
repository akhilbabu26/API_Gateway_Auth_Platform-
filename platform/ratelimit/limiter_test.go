package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	allowed   bool
	remaining int
	retry     time.Duration
	err       error
	keys      []string
}

func (f *fakeStore) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, int, time.Duration, error) {
	f.keys = append(f.keys, key)
	return f.allowed, f.remaining, f.retry, f.err
}

func TestMiddlewareReturns429WithRateHeaders(t *testing.T) {
	store := &fakeStore{remaining: 0, retry: 1500 * time.Millisecond}
	mw, err := NewMiddleware(store, Config{Enabled: true, KeySecret: "test-key"}, Policy{Name: "global", Limit: 1, Window: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
	for name, want := range map[string]string{"X-RateLimit-Limit": "1", "X-RateLimit-Remaining": "0", "Retry-After": "2"} {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.Contains(recorder.Body.String(), `"error":"RATE_LIMIT_EXCEEDED"`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-RateLimit-Reset") == "" {
		t.Fatal("missing reset header")
	}
}

func TestMiddlewareFailModes(t *testing.T) {
	for _, tc := range []struct {
		mode FailureMode
		want int
	}{{FailOpen, http.StatusNoContent}, {FailClosed, http.StatusServiceUnavailable}} {
		store := &fakeStore{err: errors.New("private redis details")}
		mw, err := NewMiddleware(store, Config{Enabled: true, FailureMode: tc.mode, KeySecret: "test-key"}, Policy{Name: "global", Limit: 10, Window: time.Minute}, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != tc.want {
			t.Errorf("mode %s: status = %d, want %d", tc.mode, recorder.Code, tc.want)
		}
		if strings.Contains(recorder.Body.String(), "private redis details") {
			t.Fatal("leaked backend error")
		}
	}
}

func TestTrustedProxyResolutionAndHeaderSpoofing(t *testing.T) {
	store := &fakeStore{allowed: true, remaining: 9}
	mw, err := NewMiddleware(store, Config{Enabled: true, KeySecret: "test-key", TrustedProxy: []string{"10.0.0.0/8"}}, Policy{Name: "global", Limit: 10, Window: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct{ remote, xff string }{
		{"198.51.100.8:1234", "203.0.113.50"}, // Untrusted peer cannot spoof XFF.
		{"10.0.0.4:1234", "203.0.113.50"},     // Trusted proxy's client address is accepted.
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		req.Header.Set("X-Forwarded-For", tc.xff)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	if store.keys[0] == store.keys[1] {
		t.Fatal("trusted proxy resolution did not distinguish effective client IPs")
	}
}

func TestAuthenticatedIdentityUsesSeparateKey(t *testing.T) {
	store := &fakeStore{allowed: true, remaining: 9}
	identity := func(*http.Request) (string, bool) { return "user-42", true }
	mw, err := NewMiddleware(store, Config{Enabled: true, KeySecret: "test-key"}, Policy{Name: "general", Limit: 10, Window: time.Minute}, identity)
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if len(store.keys) != 1 || !strings.HasPrefix(store.keys[0], "ratelimit:general:") {
		t.Fatalf("unexpected key: %v", store.keys)
	}
	if strings.Contains(store.keys[0], "user-42") || strings.Contains(store.keys[0], "192.0.2.1") {
		t.Fatalf("identity leaked in key: %s", store.keys[0])
	}
}

func TestDisabledMiddlewareBypassesStore(t *testing.T) {
	store := &fakeStore{allowed: false}
	mw, err := NewMiddleware(store, Config{Enabled: false, KeySecret: "test-key"}, Policy{Name: "global", Limit: 1, Window: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent || len(store.keys) != 0 {
		t.Fatalf("disabled middleware called store or rejected request")
	}
}
