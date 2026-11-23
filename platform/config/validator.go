package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// AppConfig holds validated application runtime configuration.
type AppConfig struct {
	AppEnv         string
	Port           string
	DatabaseURL    string
	RedisURL       string
	JWTSecret      string
	JWTIssuer      string
	AllowedOrigins []string
	PostgresUser   string
	PostgresDB     string
}

// RateLimitConfig is shared by gateway processes and is intentionally independent of auth settings.
type RateLimitConfig struct {
	Enabled           bool
	Algorithm         string
	GlobalLimit       int
	GlobalWindow      time.Duration
	LoginLimit        int
	RegistrationLimit int
	RefreshLimit      int
	FailMode          string
	TrustedProxies    []string
	KeySecret         string
}

// LoadAndValidateConfig loads environment variables and strictly validates critical secrets and CORS.
func LoadAndValidateConfig() (*AppConfig, error) {
	// Auto-load .env files if present (service dir or workspace root)
	_ = LoadEnv(".env", "../../.env")

	return ValidateConfigFromEnv()
}

// ValidateConfigFromEnv validates the current environment configuration without re-reading .env files.
func ValidateConfigFromEnv() (*AppConfig, error) {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if appEnv == "" {
		appEnv = "development"
	}

	port := os.Getenv("AUTH_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8081"
	}

	pgUser := strings.TrimSpace(os.Getenv("POSTGRES_USER"))
	pgPass := strings.TrimSpace(os.Getenv("POSTGRES_PASSWORD"))
	pgHost := strings.TrimSpace(os.Getenv("POSTGRES_HOST"))
	pgPort := strings.TrimSpace(os.Getenv("POSTGRES_PORT"))
	pgDB := strings.TrimSpace(os.Getenv("POSTGRES_DB"))
	sslMode := strings.TrimSpace(os.Getenv("POSTGRES_SSLMODE"))
	if sslMode == "" {
		sslMode = "disable"
	}

	// Database URL resolution precedence:
	// 1. Explicit DATABASE_URL
	// 2. Fallback to AUTH_DATABASE_URL if DATABASE_URL is not set
	// 3. Construct from POSTGRES_* fields
	// 4. Fallback default for seamless local development (non-production only)
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("AUTH_DATABASE_URL"))
	}
	if dbURL == "" {
		if pgHost != "" && pgDB != "" && pgUser != "" {
			dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", pgUser, pgPass, pgHost, pgPort, pgDB, sslMode)
		} else if appEnv != "production" {
			dbURL = "postgres://postgres:123@localhost:5432/apigateway_auth?sslmode=disable"
		}
	}

	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		redisHost := os.Getenv("REDIS_HOST")
		redisPort := os.Getenv("REDIS_PORT")
		redisPass := os.Getenv("REDIS_PASSWORD")
		if redisHost != "" && redisPort != "" {
			if redisPass != "" {
				redisURL = fmt.Sprintf("redis://:%s@%s:%s", redisPass, redisHost, redisPort)
			} else {
				redisURL = fmt.Sprintf("redis://%s:%s", redisHost, redisPort)
			}
		} else {
			redisURL = "redis://localhost:6379"
		}
	}

	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret == "" {
		jwtSecret = strings.TrimSpace(os.Getenv("AUTH_JWT_SECRET"))
	}
	if jwtSecret == "" && appEnv != "production" {
		jwtSecret = "super-secret-production-grade-hmac-key-256bit"
	}

	jwtIssuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if jwtIssuer == "" {
		jwtIssuer = "apigateway-auth-service"
	}

	// 2. Parse & Validate CORS Allowed Origins
	var allowedOrigins []string
	rawOrigins := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if rawOrigins != "" {
		parts := strings.Split(rawOrigins, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				allowedOrigins = append(allowedOrigins, trimmed)
			}
		}
	}

	// 3. Strict Production Secrets & Credentials Validation
	if appEnv == "production" {
		if pgUser == "" {
			return nil, errors.New("config: POSTGRES_USER is required in production")
		}
		if pgPass == "" || pgPass == "123" || pgPass == "postgrespassword" {
			return nil, errors.New("config: secure POSTGRES_PASSWORD is required in production")
		}
		if pgDB == "" {
			return nil, errors.New("config: POSTGRES_DB is required in production")
		}
		if dbURL == "" {
			return nil, errors.New("config: DATABASE_URL is required in production")
		}
		if strings.Contains(dbURL, ":123@") || strings.Contains(dbURL, ":postgrespassword@") {
			return nil, errors.New("config: default development password detected in DATABASE_URL; please supply a secure production password")
		}

		// Ensure DATABASE_URL credentials match POSTGRES_* configuration
		parsedURL, err := url.Parse(dbURL)
		if err == nil {
			if parsedURL.User != nil {
				if parsedURL.User.Username() != pgUser {
					return nil, fmt.Errorf("config: DATABASE_URL username (%s) does not match POSTGRES_USER (%s)", parsedURL.User.Username(), pgUser)
				}
				pwd, hasPwd := parsedURL.User.Password()
				if !hasPwd || pwd == "" || pwd == "123" || pwd == "postgrespassword" {
					return nil, errors.New("config: secure password required in DATABASE_URL for production")
				}
				if pgPass != "" && pwd != pgPass {
					return nil, errors.New("config: DATABASE_URL password does not match POSTGRES_PASSWORD")
				}
			}
			dbPath := strings.TrimPrefix(parsedURL.Path, "/")
			if pgDB != "" && dbPath != "" && dbPath != pgDB {
				return nil, fmt.Errorf("config: DATABASE_URL database (%s) does not match POSTGRES_DB (%s)", dbPath, pgDB)
			}
		}

		if jwtSecret == "" || jwtSecret == "super-secret-production-grade-hmac-key-256bit" || jwtSecret == "replace_with_min_32_char_cryptographically_secure_random_key_for_production" || len(jwtSecret) < 32 {
			return nil, errors.New("config: insecure or default JWT_SECRET detected in production; must be at least 32 characters long")
		}

		// Production CORS Rule: NO wildcard fallback allowed in production
		if len(allowedOrigins) == 0 {
			return nil, errors.New("config: CORS_ALLOWED_ORIGINS must be explicitly configured in production (e.g. https://your-frontend.vercel.app)")
		}
		for _, origin := range allowedOrigins {
			if origin == "*" {
				return nil, errors.New("config: wildcard '*' CORS origin is forbidden in production; specify explicit frontend domain(s)")
			}
		}
	} else {
		// Development fallback
		if len(allowedOrigins) == 0 {
			allowedOrigins = []string{"http://localhost:3000", "http://localhost:5173", "http://127.0.0.1:3000", "http://127.0.0.1:5173"}
		}
	}

	return &AppConfig{
		AppEnv:         appEnv,
		Port:           port,
		DatabaseURL:    dbURL,
		RedisURL:       redisURL,
		JWTSecret:      jwtSecret,
		JWTIssuer:      jwtIssuer,
		AllowedOrigins: allowedOrigins,
		PostgresUser:   pgUser,
		PostgresDB:     pgDB,
	}, nil
}

// LoadRateLimitConfigFromEnv reads gateway rate-limit settings independently from auth configuration.
func LoadRateLimitConfigFromEnv() (RateLimitConfig, error) {
	config := RateLimitConfig{
		Enabled:           true,
		Algorithm:         "sliding_window",
		GlobalLimit:       100,
		GlobalWindow:      time.Minute,
		LoginLimit:        10,
		RegistrationLimit: 5,
		RefreshLimit:      20,
		FailMode:          "closed",
		KeySecret:         strings.TrimSpace(os.Getenv("RATE_LIMIT_KEY_SECRET")),
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_ENABLED")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return config, fmt.Errorf("config: invalid RATE_LIMIT_ENABLED: %w", err)
		}
		config.Enabled = value
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_ALGORITHM")); raw != "" {
		config.Algorithm = strings.ToLower(raw)
	}
	if config.Algorithm != "sliding_window" {
		return config, fmt.Errorf("config: RATE_LIMIT_ALGORITHM must be sliding_window")
	}
	for _, item := range []struct {
		name string
		dest *int
	}{{"RATE_LIMIT_GLOBAL_LIMIT", &config.GlobalLimit}, {"RATE_LIMIT_LOGIN_LIMIT", &config.LoginLimit}, {"RATE_LIMIT_REGISTER_LIMIT", &config.RegistrationLimit}, {"RATE_LIMIT_REFRESH_LIMIT", &config.RefreshLimit}} {
		if raw := strings.TrimSpace(os.Getenv(item.name)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				return config, fmt.Errorf("config: %s must be a positive integer", item.name)
			}
			*item.dest = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_WINDOW")); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil || window < time.Millisecond {
			return config, fmt.Errorf("config: RATE_LIMIT_WINDOW must be at least 1ms")
		}
		config.GlobalWindow = window
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_FAIL_MODE")); raw != "" {
		config.FailMode = strings.ToLower(raw)
	}
	if config.FailMode != "open" && config.FailMode != "closed" {
		return config, fmt.Errorf("config: RATE_LIMIT_FAIL_MODE must be open or closed")
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_TRUSTED_PROXIES")); raw != "" {
		for _, proxy := range strings.Split(raw, ",") {
			if proxy = strings.TrimSpace(proxy); proxy != "" {
				config.TrustedProxies = append(config.TrustedProxies, proxy)
			}
		}
	}
	if config.Enabled && len(config.KeySecret) < 32 {
		return config, fmt.Errorf("config: RATE_LIMIT_KEY_SECRET must be at least 32 characters when rate limiting is enabled")
	}
	return config, nil
}
