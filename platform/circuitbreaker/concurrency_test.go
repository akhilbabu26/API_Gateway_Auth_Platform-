package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrencyHeavyTraffic(t *testing.T) {
	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 10,
		Cooldown:         50 * time.Millisecond,
		HalfOpenMaxCalls: 5,
		Clock:            clock,
	})

	goroutines := 100
	iterations := 200
	var wg sync.WaitGroup

	var admittedCount int64
	var rejectedCount int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				permit, err := breaker.Allow()
				if err != nil {
					atomic.AddInt64(&rejectedCount, 1)
					continue
				}
				atomic.AddInt64(&admittedCount, 1)

				// Distribute outcomes
				switch (id + j) % 5 {
				case 0, 1:
					permit.Success()
				case 2:
					permit.Failure()
				case 3:
					permit.Abandon()
				case 4:
					// Test duplicate calls
					permit.Success()
					permit.Failure()
				}
			}
		}(i)
	}

	wg.Wait()

	total := admittedCount + rejectedCount
	expected := int64(goroutines * iterations)
	if total != expected {
		t.Fatalf("total requests = %d, want %d", total, expected)
	}
}

func TestConcurrencyHalfOpenLimitStrictlyEnforced(t *testing.T) {
	clock := newMockClock(time.Now())
	maxTrials := 3
	breaker := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: maxTrials,
		Clock:            clock,
	})

	// Trip to OPEN
	p, _ := breaker.Allow()
	p.Failure()

	// Advance clock past cooldown
	clock.Advance(10 * time.Second)

	// 50 goroutines simultaneously compete for the 3 trial slots
	goroutines := 50
	var wg sync.WaitGroup
	var admittedTrials int64
	var rejectedTrials int64

	var maxObservedInFlight int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := breaker.Allow()
			if err != nil {
				atomic.AddInt64(&rejectedTrials, 1)
				return
			}
			curInFlight := int64(breaker.HalfOpenInFlight())
			for {
				old := atomic.LoadInt64(&maxObservedInFlight)
				if curInFlight <= old || atomic.CompareAndSwapInt64(&maxObservedInFlight, old, curInFlight) {
					break
				}
			}

			atomic.AddInt64(&admittedTrials, 1)
			time.Sleep(1 * time.Millisecond)
			permit.Abandon()
		}()
	}

	wg.Wait()

	if admittedTrials < int64(maxTrials) {
		t.Fatalf("admitted trials = %d, expected at least %d", admittedTrials, maxTrials)
	}
	if maxObservedInFlight > int64(maxTrials) {
		t.Fatalf("max observed in-flight = %d, exceeded limit %d", maxObservedInFlight, maxTrials)
	}
}

func TestConcurrencyStalePermitCannotCorruptRecoveredCircuit(t *testing.T) {
	clock := newMockClock(time.Now())
	breaker := New(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		HalfOpenMaxCalls: 2,
		Clock:            clock,
	})

	// Trip to OPEN
	pInit, _ := breaker.Allow()
	pInit.Failure()

	clock.Advance(10 * time.Second)

	// In HALF-OPEN: Issue Permit A and Permit B
	permitA, err := breaker.Allow()
	if err != nil {
		t.Fatalf("permitA err = %v", err)
	}
	permitB, err := breaker.Allow()
	if err != nil {
		t.Fatalf("permitB err = %v", err)
	}

	// Permit A succeeds first -> Breaker transitions to CLOSED (Generation advances)
	permitA.Success()
	if breaker.State() != StateClosed {
		t.Fatalf("state after permitA success = %v, want CLOSED", breaker.State())
	}

	// Slower Permit B fails -> Belongs to previous generation!
	permitB.Failure()

	// Breaker MUST remain CLOSED, not OPEN!
	if breaker.State() != StateClosed {
		t.Fatalf("stale failure reopened recovered circuit! state = %v, want CLOSED", breaker.State())
	}
}
