package config

import "testing"

func TestRateLimitConfigDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_ALGORITHM", "sliding_window")
	t.Setenv("RATE_LIMIT_GLOBAL_LIMIT", "120")
	t.Setenv("RATE_LIMIT_WINDOW", "2m")
	t.Setenv("RATE_LIMIT_FAIL_MODE", "open")
	t.Setenv("RATE_LIMIT_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1")
	t.Setenv("RATE_LIMIT_KEY_SECRET", "local-test-secret-that-is-long-enough-32")
	cfg, err := LoadRateLimitConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.GlobalLimit != 120 || cfg.GlobalWindow.String() != "2m0s" || cfg.FailMode != "open" || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestRateLimitConfigRequiresKeySecretWhenEnabled(t *testing.T) {
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_KEY_SECRET", "too-short")
	if _, err := LoadRateLimitConfigFromEnv(); err == nil {
		t.Fatal("expected missing key secret to fail")
	}
}
