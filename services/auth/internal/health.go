package internal

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthHandler manages liveness and readiness probes.
type HealthHandler struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	appEnv string
}

// NewHealthHandler creates a HealthHandler instance.
func NewHealthHandler(pool *pgxpool.Pool, rdb *redis.Client, appEnv string) *HealthHandler {
	return &HealthHandler{
		pool:   pool,
		rdb:    rdb,
		appEnv: appEnv,
	}
}

// Liveness (/healthz) returns 200 if the HTTP process is alive.
func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","env":"` + h.appEnv + `"}`))
}

// Readiness (/readyz) verifies that required backing resources (PostgreSQL, Redis) are operational.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// Check PostgreSQL
	if h.pool == nil || h.pool.Ping(ctx) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable","database":"down"}`))
		return
	}

	// Check Redis
	if h.rdb == nil || h.rdb.Ping(ctx).Err() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable","redis":"down"}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready","database":"up","redis":"up"}`))
}
