package config

import (
	"os"
	"testing"
)

func TestConfigValidation_Development(t *testing.T) {
	_ = os.Setenv("APP_ENV", "development")
	_ = os.Setenv("JWT_SECRET", "short")
	_ = os.Setenv("DATABASE_URL", "postgres://postgres:123@localhost:5432/apigateway_auth?sslmode=disable")
	_ = os.Unsetenv("CORS_ALLOWED_ORIGINS")

	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("expected development config to load without error, got: %v", err)
	}

	if cfg.Port != "8081" {
		t.Errorf("expected default port 8081, got %s", cfg.Port)
	}
	if len(cfg.AllowedOrigins) == 0 || cfg.AllowedOrigins[0] == "*" {
		t.Errorf("expected safe localhost dev origins, got %v", cfg.AllowedOrigins)
	}
}

func TestConfigValidation_ProductionSafety(t *testing.T) {
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("POSTGRES_USER", "prod_admin")
	_ = os.Setenv("POSTGRES_PASSWORD", "a_very_strong_prod_db_pass_12345")
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "https://app.vercel.app")

	// Test 1: Insecure default DB password rejected in DATABASE_URL
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:123@localhost:5432/apigateway_auth")
	_ = os.Setenv("JWT_SECRET", "this-is-a-very-long-production-key-over-32-chars")
	_, err := LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error on default DB password '123'")
	}

	// Test 2: Mismatched POSTGRES_USER and DATABASE_URL username rejected
	_ = os.Setenv("POSTGRES_USER", "prod_admin")
	_ = os.Setenv("DATABASE_URL", "postgres://different_user:a_very_strong_prod_db_pass_12345@localhost:5432/apigateway_auth")
	_, err = LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error when POSTGRES_USER does not match DATABASE_URL user")
	}

	// Test 3: Insecure default JWT secret rejected
	_ = os.Setenv("POSTGRES_USER", "prod_admin")
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:a_very_strong_prod_db_pass_12345@localhost:5432/apigateway_auth")
	_ = os.Setenv("JWT_SECRET", "super-secret-production-grade-hmac-key-256bit")
	_, err = LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error on default placeholder JWT_SECRET")
	}

	// Test 4: Short JWT secret rejected
	_ = os.Setenv("JWT_SECRET", "short_secret")
	_, err = LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error on short JWT_SECRET")
	}

	// Test 5: Wildcard CORS rejected in production
	_ = os.Setenv("JWT_SECRET", "a_very_strong_cryptographically_secure_random_production_secret_key_12345")
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "*")
	_, err = LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error when CORS_ALLOWED_ORIGINS is '*'")
	}

	// Test 6: Missing CORS rejected in production
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "")
	_, err = ValidateConfigFromEnv()
	if err == nil {
		t.Fatal("expected production error when CORS_ALLOWED_ORIGINS is missing in production")
	}

	// Test 7: Missing POSTGRES_USER rejected in production
	_ = os.Setenv("POSTGRES_USER", "")
	_ = os.Setenv("POSTGRES_PASSWORD", "a_very_strong_prod_db_pass_12345")
	_ = os.Setenv("POSTGRES_DB", "apigateway_auth")
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:a_very_strong_prod_db_pass_12345@localhost:5432/apigateway_auth")
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "https://app.vercel.app")
	_, err = ValidateConfigFromEnv()
	if err == nil {
		t.Fatal("expected production error when POSTGRES_USER is missing in production")
	}

	// Test 8: Missing POSTGRES_DB rejected in production
	_ = os.Setenv("POSTGRES_USER", "prod_admin")
	_ = os.Setenv("POSTGRES_PASSWORD", "a_very_strong_prod_db_pass_12345")
	_ = os.Setenv("POSTGRES_DB", "")
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:a_very_strong_prod_db_pass_12345@localhost:5432/apigateway_auth")
	_ = os.Setenv("JWT_SECRET", "a_very_strong_cryptographically_secure_random_production_secret_key_12345")
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "https://app.vercel.app")
	_, err = ValidateConfigFromEnv()
	if err == nil {
		t.Fatal("expected production error when POSTGRES_DB is missing in production")
	}

	// Test 9: Mismatched POSTGRES_PASSWORD rejected in production
	_ = os.Setenv("POSTGRES_DB", "apigateway_auth")
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:different_password@localhost:5432/apigateway_auth")
	_, err = LoadAndValidateConfig()
	if err == nil {
		t.Fatal("expected production error when DATABASE_URL password does not match POSTGRES_PASSWORD")
	}

	// Test 10: Valid production configuration passes
	_ = os.Setenv("POSTGRES_USER", "prod_admin")
	_ = os.Setenv("POSTGRES_PASSWORD", "a_very_strong_prod_db_pass_12345")
	_ = os.Setenv("POSTGRES_DB", "apigateway_auth")
	_ = os.Setenv("DATABASE_URL", "postgres://prod_admin:a_very_strong_prod_db_pass_12345@localhost:5432/apigateway_auth?sslmode=disable")
	_ = os.Setenv("JWT_SECRET", "a_very_strong_cryptographically_secure_random_production_secret_key_12345")
	_ = os.Setenv("CORS_ALLOWED_ORIGINS", "https://app.vercel.app,https://admin.vercel.app")
	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("expected valid production config to pass, got: %v", err)
	}
	if cfg.AppEnv != "production" {
		t.Errorf("expected production env, got %s", cfg.AppEnv)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Errorf("expected 2 allowed origins, got %d", len(cfg.AllowedOrigins))
	}

	// Cleanup
	_ = os.Setenv("APP_ENV", "development")
	_ = os.Unsetenv("JWT_SECRET")
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Unsetenv("CORS_ALLOWED_ORIGINS")
	_ = os.Unsetenv("POSTGRES_USER")
	_ = os.Unsetenv("POSTGRES_PASSWORD")
	_ = os.Unsetenv("POSTGRES_DB")
}
