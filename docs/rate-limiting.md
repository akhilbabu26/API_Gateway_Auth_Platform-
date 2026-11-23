# Gateway rate limiting

The shared limiter lives in `platform/ratelimit`; it is infrastructure for the API Gateway and is not mounted by the Auth Service. This checkout currently has no Gateway process or Gateway Chi router, so there is no router to wire it into yet. When that router is added, mount the global middleware after request ID and before authentication, then mount identity-aware endpoint policies after authentication where appropriate.

## Algorithm and Redis behavior

`RedisStore` uses a sliding-window log backed by a Redis sorted set. A Lua script prunes expired timestamps, checks the active count, inserts the current request, and sets key expiry atomically. That avoids fixed-window boundary bursts and ensures replicas using the same Redis database enforce one shared limit. The tradeoff is that each active identity stores one sorted-set member per request during the window.

Use `NewRedisStore` with the process's existing `*redis.Client` from `platform/redis`. `NewRedis` remains as a compatibility helper for standalone callers that need an owned connection. Redis keys contain a policy name and an HMAC-SHA256 digest of the client identity; the IP address and user ID are not stored in clear text.

## Configuration

`platform/config.LoadRateLimitConfigFromEnv` reads the settings below independently from auth configuration. `RATE_LIMIT_KEY_SECRET` is required when enabled. Trusted proxy addresses accept IPs or CIDRs; by default no proxy is trusted, so caller-supplied `X-Forwarded-For` is ignored.

```dotenv
RATE_LIMIT_ENABLED=true
RATE_LIMIT_ALGORITHM=sliding_window
RATE_LIMIT_GLOBAL_LIMIT=100
RATE_LIMIT_WINDOW=1m
RATE_LIMIT_LOGIN_LIMIT=10
RATE_LIMIT_REGISTER_LIMIT=5
RATE_LIMIT_REFRESH_LIMIT=20
RATE_LIMIT_FAIL_MODE=closed
RATE_LIMIT_TRUSTED_PROXIES=10.0.0.0/8,192.168.1.10
RATE_LIMIT_KEY_SECRET=<separate-random-secret>
```

The endpoint values are limits per the configured window. Create a middleware per policy, for example `Policy{Name: "login", Limit: cfg.LoginLimit, Window: cfg.GlobalWindow}`. The global policy should run before route-specific policies. `IdentityFunc` can return an authenticated user ID for a per-user policy; if it returns no identity, the middleware falls back to trusted client IP resolution.

On rejection the middleware returns `429` with `success: false`, `error: RATE_LIMIT_EXCEEDED`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, and `Retry-After`. With `RATE_LIMIT_FAIL_MODE=closed`, Redis errors return `503`; with `open`, requests continue and a policy-only failure is logged without Redis error text or identity data.

## Chi integration example

```go
store, err := ratelimit.NewRedisStore(sharedRedisClient)
if err != nil { return err }
limiterConfig := ratelimit.Config{
    Enabled: cfg.Enabled,
    FailureMode: ratelimit.FailureMode(cfg.FailMode),
    TrustedProxy: cfg.TrustedProxies,
    KeySecret: cfg.KeySecret,
}
global, err := ratelimit.NewMiddleware(store, limiterConfig, ratelimit.Policy{
    Name: "global", Limit: cfg.GlobalLimit, Window: cfg.GlobalWindow,
}, nil)
if err != nil { return err }
router.Use(global)

login, err := ratelimit.NewMiddleware(store, limiterConfig, ratelimit.Policy{
    Name: "login", Limit: cfg.LoginLimit, Window: cfg.GlobalWindow,
}, nil)
if err != nil { return err }
router.With(login).Post("/auth/login", loginHandler)
```

Pass an identity function instead of `nil` for policies that should use authenticated user IDs. Mount those policies after the authentication middleware so identity is available. Never trust forwarded headers unless the immediate peer is in `RATE_LIMIT_TRUSTED_PROXIES`.

## Verification

Unit tests cover response headers and body, failure modes, disabled operation, trusted-proxy handling, and user-key anonymization. Set `RATE_LIMIT_TEST_REDIS_URL` to run the Redis integration test for shared state across two clients, window expiry, and concurrent atomic admission.

Example:

```sh
curl -i http://localhost:8080/api/resource
```

After the configured quota is consumed, the response is `429 Too Many Requests` and includes a `Retry-After` value.
