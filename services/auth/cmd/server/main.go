package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"auth/internal"
	"auth/internal/mail"
	"auth/internal/otp"
	"platform/config"
	"platform/hasher"
	"platform/jwt"
	"platform/postgres"
	"platform/redis"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	// Parse CLI flags (used by Docker healthcheck without curl/wget dependencies)
	healthCheckFlag := flag.Bool("healthcheck", false, "Query the local healthcheck endpoint and exit")
	flag.Parse()

	if *healthCheckFlag {
		port := os.Getenv("AUTH_PORT")
		if port == "" {
			port = os.Getenv("PORT")
		}
		if port == "" {
			port = "8081"
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 1. Load and Validate Configuration (Fails fast if production secrets/CORS are invalid)
	cfg, err := config.LoadAndValidateConfig()
	if err != nil {
		log.Fatalf("Fatal configuration error: %v\n", err)
	}
	log.Printf("Starting Auth Service in [%s] mode on port %s...\n", cfg.AppEnv, cfg.Port)

	// 2. Connect to PostgreSQL (Fatal if unavailable - ensures readiness before accepting traffic)
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	pool, err := postgres.NewPool(dbCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Fatal: PostgreSQL connection failed: %v\n", err)
	}
	defer pool.Close()
	log.Println("Connected to PostgreSQL successfully.")

	// 3. Run Database Migrations (Fatal if migrations fail)
	if err := internal.RunMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("Fatal: Database migration failed: %v\n", err)
	}
	log.Println("Database migrations executed successfully (Goose UP).")

	// 4. Connect to Redis (Fatal if unavailable - OTP and token tracking depend on Redis)
	rdb, err := redis.NewClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("Fatal: Redis connection failed on %s: %v\n", cfg.RedisURL, err)
	}
	defer rdb.Close()
	log.Println("Connected to Redis successfully.")

	// 5. Initialize Components
	repo := internal.NewPostgresRepository(pool)
	passHasher := hasher.NewPasswordHasher(nil)
	tokenMgr := jwt.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, 15*time.Minute, 7*24*time.Hour)
	otpMgr := otp.NewManager(rdb, 5*time.Minute)
	mailer := mail.NewLogMailer()

	authSvc := internal.NewService(repo, passHasher, tokenMgr, otpMgr, mailer)
	authHandler := internal.NewHandler(authSvc)
	healthHandler := internal.NewHealthHandler(pool, rdb, cfg.AppEnv)

	// 6. Router & Middleware Stack
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(internal.CORS(cfg.AllowedOrigins))

	// Liveness probe (Process is alive)
	r.Get("/healthz", healthHandler.Liveness)

	// Readiness probe (Backing databases and cache are connected and responsive)
	r.Get("/readyz", healthHandler.Readiness)

	// Auth API routes
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/verify-email", authHandler.VerifyEmail)
		r.Post("/resend-verification", authHandler.ResendVerification)
		r.Post("/forgot-password", authHandler.ForgotPassword)
		r.Post("/reset-password", authHandler.ResetPassword)
		r.Post("/login", authHandler.Login)
		r.Post("/refresh", authHandler.Refresh)
		r.Post("/logout", authHandler.Logout)
	})

	// 7. Explicit Bind on 0.0.0.0 for Containers & Cloud Instances
	server := &http.Server{
		Addr:         "0.0.0.0:" + cfg.Port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Auth Service listening on %s (Allowed CORS: %v)...\n", server.Addr, cfg.AllowedOrigins)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v\n", err)
		}
	}()

	// 8. Graceful OS Signal Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down auth server gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v\n", err)
	}
	log.Println("Auth server stopped cleanly.")
}
