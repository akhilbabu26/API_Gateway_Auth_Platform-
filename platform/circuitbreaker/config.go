package circuitbreaker

import (
	"fmt"
	"time"
)

// Clock abstracts time measurement for deterministic testing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// Config defines the configuration parameters for a circuit breaker instance.
type Config struct {
	// FailureThreshold is the number of consecutive upstream failures
	// required to transition from CLOSED to OPEN. Must be >= 1.
	FailureThreshold int

	// Cooldown is the duration the circuit remains OPEN before transitioning
	// to HALF_OPEN to allow trial requests. Must be > 0.
	Cooldown time.Duration

	// HalfOpenMaxCalls is the maximum number of concurrent trial requests
	// admitted while in the HALF_OPEN state. Must be >= 1.
	HalfOpenMaxCalls int

	// Clock enables deterministic testing via a mock clock. If nil, real time is used.
	Clock Clock
}

// Validate checks whether the configuration values are valid.
func (c Config) Validate() error {
	if c.FailureThreshold < 1 {
		return fmt.Errorf("circuitbreaker: FailureThreshold must be at least 1, got %d", c.FailureThreshold)
	}
	if c.Cooldown <= 0 {
		return fmt.Errorf("circuitbreaker: Cooldown must be greater than 0, got %v", c.Cooldown)
	}
	if c.HalfOpenMaxCalls < 1 {
		return fmt.Errorf("circuitbreaker: HalfOpenMaxCalls must be at least 1, got %d", c.HalfOpenMaxCalls)
	}
	return nil
}

// WithDefaults returns a copy of the config with sensible production defaults applied for unset values.
func (c Config) WithDefaults() Config {
	cfg := c
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.HalfOpenMaxCalls <= 0 {
		cfg.HalfOpenMaxCalls = 2
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return cfg
}
