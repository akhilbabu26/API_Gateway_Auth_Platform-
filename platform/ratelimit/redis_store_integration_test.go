package ratelimit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Set RATE_LIMIT_TEST_REDIS_URL to run this integration test against Redis.
func TestRedisStoreSharedAtomicWindowAndExpiry(t *testing.T) {
	redisURL := os.Getenv("RATE_LIMIT_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("RATE_LIMIT_TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal("invalid test Redis URL")
	}
	clientA, clientB := redis.NewClient(opts), redis.NewClient(opts)
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clientA.Ping(ctx).Err(); err != nil {
		t.Fatal("Redis unavailable")
	}
	storeA, _ := NewRedisStore(clientA)
	storeB, _ := NewRedisStore(clientB)
	key := fmt.Sprintf("ratelimit:test:%d", time.Now().UnixNano())

	allowed, remaining, _, err := storeA.Allow(ctx, key, 1, 250*time.Millisecond)
	if err != nil || !allowed || remaining != 0 {
		t.Fatalf("first request: allowed=%v remaining=%d err=%v", allowed, remaining, err)
	}
	allowed, _, _, err = storeB.Allow(ctx, key, 1, 250*time.Millisecond)
	if err != nil || allowed {
		t.Fatalf("shared second request should be denied: allowed=%v err=%v", allowed, err)
	}
	time.Sleep(300 * time.Millisecond)
	allowed, _, _, err = storeB.Allow(ctx, key, 1, 250*time.Millisecond)
	if err != nil || !allowed {
		t.Fatalf("request after expiry should pass: allowed=%v err=%v", allowed, err)
	}

	concurrentKey := key + ":concurrent"
	var admitted atomic.Int64
	var wait sync.WaitGroup
	for i := 0; i < 64; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			store := storeA
			if i%2 == 1 {
				store = storeB
			}
			ok, _, _, err := store.Allow(ctx, concurrentKey, 7, time.Second)
			if err != nil {
				t.Errorf("concurrent Redis request failed")
			}
			if ok {
				admitted.Add(1)
			}
		}(i)
	}
	wait.Wait()
	if got := admitted.Load(); got != 7 {
		t.Fatalf("concurrent requests admitted %d, want exactly 7", got)
	}
}
