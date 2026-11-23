package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a compatibility wrapper for callers that need a self-owned client.
// Gateway processes should prefer NewRedisStore with their existing shared client.
type Redis struct {
	client *redis.Client
	store  *RedisStore
}

func NewRedis(redisURL string, timeout time.Duration) (*Redis, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %w", err)
	}
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	opts.DialTimeout = timeout
	opts.ReadTimeout = timeout
	opts.WriteTimeout = timeout
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to Redis: %w", err)
	}
	store, err := NewRedisStore(client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Redis{client: client, store: store}, nil
}

func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	return r.store.Allow(ctx, key, limit, window)
}

func (r *Redis) Close() error { return r.client.Close() }
