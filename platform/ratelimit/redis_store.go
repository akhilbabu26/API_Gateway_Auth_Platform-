package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore uses a sorted-set sliding window. Lua keeps prune/count/add atomic
// across every gateway instance sharing this Redis client/database.
type RedisStore struct {
	client redis.UniversalClient
}

func NewRedisStore(client redis.UniversalClient) (*RedisStore, error) {
	if client == nil {
		return nil, fmt.Errorf("Redis client is required")
	}
	return &RedisStore{client: client}, nil
}

const slidingWindowScript = `
local t = redis.call('TIME')
local now = t[1] * 1000 + math.floor(t[2] / 1000)
local window = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
local count = redis.call('ZCARD', KEYS[1])
local allowed = 0
if count < limit then
  redis.call('ZADD', KEYS[1], now, ARGV[3])
  count = count + 1
  allowed = 1
end
local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local retry = math.max(1, tonumber(first[2]) + window - now)
redis.call('PEXPIRE', KEYS[1], window)
return {allowed, count, retry}
`

func (s *RedisStore) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	if limit < 1 || window < time.Millisecond {
		return false, 0, 0, fmt.Errorf("limit must be positive and window must be at least one millisecond")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, 0, 0, fmt.Errorf("generate rate limit request identifier: %w", err)
	}
	result, err := s.client.Eval(ctx, slidingWindowScript, []string{key}, window.Milliseconds(), limit, hex.EncodeToString(nonce[:])).Result()
	if err != nil {
		return false, 0, 0, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return false, 0, 0, fmt.Errorf("unexpected Redis sliding window response")
	}
	allowedValue, err := redisInt64(values[0])
	if err != nil {
		return false, 0, 0, err
	}
	count, err := redisInt64(values[1])
	if err != nil {
		return false, 0, 0, err
	}
	retryMillis, err := redisInt64(values[2])
	if err != nil {
		return false, 0, 0, err
	}
	remaining := limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return allowedValue == 1, remaining, time.Duration(retryMillis) * time.Millisecond, nil
}

func redisInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}
