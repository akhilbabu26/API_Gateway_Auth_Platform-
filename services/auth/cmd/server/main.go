package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"auth/internal"
	"platform/hasher"
	"platform/jwt"
	"platform/postgres"
	"platform/ratelimit"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgrespassword@localhost:5432/apigateway?sslmode=disable"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "super-secret-production-grade-hmac-key-256bit"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Connection Pool via platform/postgres
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		log.Printf("Notice: DB connection pending (start Postgres container): %v\n", err)
	} else {
		defer pool.Close()
		log.Println("Connected to PostgreSQL successfully.")
	}

	// 2. Initialize Shared Platform & Service Modules
	var repo internal.Repository
	if pool != nil {
		repo = internal.NewPostgresRepository(pool)
	}
	passHasher := hasher.NewPasswordHasher(nil)
	tokenMgr := jwt.NewTokenManager(jwtSecret, "apigateway-auth-service", 15*time.Minute, 7*24*time.Hour)
	authSvc := internal.NewService(repo, passHasher, tokenMgr)
	handler := internal.NewHandler(authSvc)

	// 3. Routing
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	limiter, err := ratelimit.NewRedis(redisURL, 250*time.Millisecond)
	if err != nil {
		log.Fatalf("Invalid REDIS_URL: %v", err)
	}

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/auth", func(r chi.Router) {
		r.With(rateLimit(limiter, 5, time.Minute, "register")).Post("/register", handler.Register)
		r.With(rateLimit(limiter, 10, time.Minute, "login")).Post("/login", handler.Login)
		r.With(rateLimit(limiter, 30, time.Minute, "refresh")).Post("/refresh", handler.Refresh)
		r.With(rateLimit(limiter, 30, time.Minute, "logout")).Post("/logout", handler.Logout)
	})

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Auth Service (TradeDrift Pattern) listening on port %s...\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v\n", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down auth server gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown failed: %v\n", err)
	}
	log.Println("Auth server stopped cleanly.")
}

func rateLimit(limiter *ratelimit.Redis, limit int, window time.Duration, route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
			allowed, remaining, retryAfter, err := limiter.Allow(r.Context(), fmt.Sprintf("auth:ratelimit:%s:%s", route, clientIP), limit, window)
			if err != nil {
				// Match the documented platform policy: Redis outages do not block auth traffic.
				log.Printf("rate limiter unavailable for %s: %v", route, err)
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Limit", fmt.Sprint(limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(remaining))
			if !allowed {
				seconds := int(math.Ceil(retryAfter.Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", fmt.Sprint(seconds))
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
