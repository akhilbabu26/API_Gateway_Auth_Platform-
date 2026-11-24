package circuitbreaker

import (
	"net/http"
	"testing"
	"time"
)

type dummyTransport struct{}

func (dummyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}, nil
}

func BenchmarkBreakerAllow_Closed(b *testing.B) {
	breaker := New(Config{
		FailureThreshold: 5,
		Cooldown:         30 * time.Second,
		HalfOpenMaxCalls: 2,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		permit, _ := breaker.Allow()
		permit.Success()
	}
}

func BenchmarkBreakerAllow_Parallel(b *testing.B) {
	breaker := New(Config{
		FailureThreshold: 5,
		Cooldown:         30 * time.Second,
		HalfOpenMaxCalls: 2,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			permit, err := breaker.Allow()
			if err == nil {
				permit.Success()
			}
		}
	})
}

func BenchmarkBreakerReport_Success(b *testing.B) {
	breaker := New(Config{
		FailureThreshold: 5,
		Cooldown:         30 * time.Second,
		HalfOpenMaxCalls: 2,
	})

	permits := make([]*Permit, b.N)
	for i := 0; i < b.N; i++ {
		permits[i], _ = breaker.Allow()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		permits[i].Success()
	}
}

func BenchmarkRoundTripper_Throughput(b *testing.B) {
	breaker := New(Config{
		FailureThreshold: 5,
		Cooldown:         30 * time.Second,
		HalfOpenMaxCalls: 2,
	})
	rt := NewRoundTripper(breaker, nil, dummyTransport{})
	req, _ := http.NewRequest(http.MethodGet, "http://localhost/test", nil)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, _ := rt.RoundTrip(req)
			if resp != nil {
				_ = resp.Body
			}
		}
	})
}
