package circuitbreaker

import (
	"fmt"
	"strings"
	"sync"
)

// Manager manages independent Circuit Breaker instances for distinct logical upstream services.
type Manager struct {
	mu         sync.RWMutex
	breakers   map[string]*Breaker
	defaultCfg Config
}

// NewManager creates a new Manager with the provided fallback configuration for unconfigured upstreams.
func NewManager(defaultCfg Config) *Manager {
	return &Manager{
		breakers:   make(map[string]*Breaker),
		defaultCfg: defaultCfg.WithDefaults(),
	}
}

// GetOrCreate returns an existing Breaker for the given upstream ID, or creates and registers
// a new Breaker if one does not already exist.
func (m *Manager) GetOrCreate(upstreamID string, customCfg ...Config) (*Breaker, error) {
	trimmedID := strings.TrimSpace(upstreamID)
	if trimmedID == "" {
		return nil, fmt.Errorf("circuitbreaker: upstreamID cannot be empty")
	}

	// Fast path: read lock
	m.mu.RLock()
	b, exists := m.breakers[trimmedID]
	m.mu.RUnlock()
	if exists {
		return b, nil
	}

	// Slow path: write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-checked locking
	if b, exists = m.breakers[trimmedID]; exists {
		return b, nil
	}

	cfg := m.defaultCfg
	if len(customCfg) > 0 {
		cfg = customCfg[0].WithDefaults()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	b = New(cfg)
	m.breakers[trimmedID] = b
	return b, nil
}

// Get returns the Breaker for the given upstream ID if present.
func (m *Manager) Get(upstreamID string) (*Breaker, bool) {
	trimmedID := strings.TrimSpace(upstreamID)
	if trimmedID == "" {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	b, exists := m.breakers[trimmedID]
	return b, exists
}

// Count returns the number of registered breakers.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.breakers)
}

// Reset re-initializes the breaker for an upstream, resetting its state to CLOSED.
func (m *Manager) Reset(upstreamID string) bool {
	trimmedID := strings.TrimSpace(upstreamID)
	m.mu.Lock()
	defer m.mu.Unlock()

	b, exists := m.breakers[trimmedID]
	if !exists {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.toClosed()
	return true
}
