package otp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrOTPNotFound         = errors.New("otp: not found or expired")
	ErrMaxAttemptsExceeded = errors.New("otp: maximum verification attempts exceeded")
)

// Manager handles generating, storing, and validating one-time passwords in Redis.
type Manager struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewManager creates a new OTP manager.
func NewManager(rdb *redis.Client, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Manager{
		rdb: rdb,
		ttl: ttl,
	}
}

// Generate generates a cryptographically secure 6-digit zero-padded OTP and stores it in Redis.
func (m *Manager) Generate(ctx context.Context, key string) (string, error) {
	// Cryptographically secure 6-digit number (000000 - 999999)
	maxVal := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, maxVal)
	if err != nil {
		return "", fmt.Errorf("failed to generate random otp: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	codeKey := fmt.Sprintf("otp:code:%s", key)
	attemptsKey := fmt.Sprintf("otp:attempts:%s", key)

	pipe := m.rdb.TxPipeline()
	pipe.Set(ctx, codeKey, code, m.ttl)
	pipe.Set(ctx, attemptsKey, 0, m.ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("failed to store otp in redis: %w", err)
	}

	return code, nil
}

// Verify verifies an input code against Redis with attempt counting and brute-force lockout.
func (m *Manager) Verify(ctx context.Context, key, inputCode string) (bool, error) {
	codeKey := fmt.Sprintf("otp:code:%s", key)
	attemptsKey := fmt.Sprintf("otp:attempts:%s", key)

	storedCode, err := m.rdb.Get(ctx, codeKey).Result()
	if errors.Is(err, redis.Nil) {
		return false, ErrOTPNotFound
	}
	if err != nil {
		return false, fmt.Errorf("redis error retrieving otp: %w", err)
	}

	// 1. Success check: single-use, clean up immediately
	if storedCode == inputCode {
		_ = m.rdb.Del(ctx, codeKey, attemptsKey).Err()
		return true, nil
	}

	// 2. Mismatch handling: increment attempts
	attempts, err := m.rdb.Incr(ctx, attemptsKey).Result()
	if err != nil {
		// Fallback check
		attemptsStr, _ := m.rdb.Get(ctx, attemptsKey).Result()
		attempts, _ = strconv.ParseInt(attemptsStr, 10, 64)
	}

	// 3. Lockout threshold: if >= 5 failed attempts, delete OTP to prevent further guessing
	if attempts >= 5 {
		_ = m.rdb.Del(ctx, codeKey, attemptsKey).Err()
		return false, ErrMaxAttemptsExceeded
	}

	return false, nil
}
