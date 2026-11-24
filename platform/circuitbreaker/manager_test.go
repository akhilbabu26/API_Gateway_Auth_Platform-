package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestManagerGetOrCreate(t *testing.T) {
	defaultCfg := Config{
		FailureThreshold: 5,
		Cooldown:         30 * time.Second,
		HalfOpenMaxCalls: 2,
	}
	m := NewManager(defaultCfg)

	t.Run("empty upstream ID rejected", func(t *testing.T) {
		_, err := m.GetOrCreate("   ")
		if err == nil {
			t.Fatal("expected error for empty upstreamID")
		}
	})

	t.Run("creates and retrieves breaker", func(t *testing.T) {
		b1, err := m.GetOrCreate("orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b1 == nil {
			t.Fatal("expected non-nil breaker")
		}

		b2, err := m.GetOrCreate("orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b1 != b2 {
			t.Fatal("expected identical breaker instance on subsequent calls")
		}

		if m.Count() != 1 {
			t.Fatalf("count = %d, want 1", m.Count())
		}
	})

	t.Run("isolates distinct upstreams", func(t *testing.T) {
		orders, _ := m.GetOrCreate("orders")
		payments, _ := m.GetOrCreate("payments")

		// Fail orders until OPEN
		for i := 0; i < 5; i++ {
			p, _ := orders.Allow()
			p.Failure()
		}

		if orders.State() != StateOpen {
			t.Fatalf("orders state = %v, want OPEN", orders.State())
		}
		// payments must remain unaffected in CLOSED
		if payments.State() != StateClosed {
			t.Fatalf("payments state = %v, want CLOSED", payments.State())
		}
	})

	t.Run("concurrent GetOrCreate is thread-safe", func(t *testing.T) {
		var wg sync.WaitGroup
		concurrency := 50
		breakers := make([]*Breaker, concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				b, _ := m.GetOrCreate("users")
				breakers[idx] = b
			}(i)
		}
		wg.Wait()

		first := breakers[0]
		for i := 1; i < concurrency; i++ {
			if breakers[i] != first {
				t.Fatalf("breaker[%d] != breakers[0]", i)
			}
		}
	})

	t.Run("reset restores closed state", func(t *testing.T) {
		orders, _ := m.Get("orders")
		if orders.State() != StateOpen {
			t.Fatalf("orders state before reset = %v, want OPEN", orders.State())
		}

		ok := m.Reset("orders")
		if !ok {
			t.Fatal("Reset('orders') returned false")
		}
		if orders.State() != StateClosed {
			t.Fatalf("orders state after reset = %v, want CLOSED", orders.State())
		}
	})
}
