package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Store atomically consumes one request from an identity's sliding window.
type Store interface {
	Allow(context.Context, string, int, time.Duration) (allowed bool, remaining int, retryAfter time.Duration, err error)
}

type FailureMode string

const (
	FailOpen   FailureMode = "open"
	FailClosed FailureMode = "closed"
)

// Policy describes one independently named rate limit.
type Policy struct {
	Name   string
	Limit  int
	Window time.Duration
}

// Config applies to all middleware created by NewMiddleware.
type Config struct {
	Enabled      bool
	FailureMode  FailureMode
	TrustedProxy []string
	KeySecret    string
	Logger       *log.Logger
}

// IdentityFunc can return an authenticated user ID after authentication middleware has run.
// An empty ID falls back to the resolved client IP.
type IdentityFunc func(*http.Request) (id string, authenticated bool)

func NewMiddleware(store Store, cfg Config, policy Policy, identity IdentityFunc) (func(http.Handler) http.Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("rate limit store is required")
	}
	if policy.Limit < 1 || policy.Window <= 0 || policy.Name == "" {
		return nil, fmt.Errorf("rate limit policy requires a name, positive limit, and positive window")
	}
	if cfg.FailureMode == "" {
		cfg.FailureMode = FailClosed
	}
	if cfg.FailureMode != FailOpen && cfg.FailureMode != FailClosed {
		return nil, fmt.Errorf("invalid rate limit failure mode %q", cfg.FailureMode)
	}
	if cfg.KeySecret == "" {
		return nil, fmt.Errorf("rate limit key secret is required")
	}
	proxies := make([]*net.IPNet, 0, len(cfg.TrustedProxy))
	for _, raw := range cfg.TrustedProxy {
		_, block, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy address %q", raw)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			block = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}
		proxies = append(proxies, block)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}
			identityValue := "ip:" + clientIP(r, proxies)
			if identity != nil {
				if id, authenticated := identity(r); authenticated && id != "" {
					identityValue = "user:" + id
				}
			}
			mac := hmac.New(sha256.New, []byte(cfg.KeySecret))
			_, _ = mac.Write([]byte(identityValue))
			key := "ratelimit:" + policy.Name + ":" + hex.EncodeToString(mac.Sum(nil))
			allowed, remaining, retryAfter, err := store.Allow(r.Context(), key, policy.Limit, policy.Window)
			if err != nil {
				if cfg.Logger != nil {
					cfg.Logger.Printf("rate limiter backend unavailable (policy=%s)", policy.Name)
				}
				if cfg.FailureMode == FailOpen {
					next.ServeHTTP(w, r)
					return
				}
				writeLimitError(w, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE", "Request admission is temporarily unavailable.")
				return
			}
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(policy.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			reset := time.Now().Add(retryAfter)
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			if !allowed {
				seconds := int64((retryAfter + time.Second - 1) / time.Second)
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
				writeLimitError(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Too many requests. Please try again later.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func clientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := net.ParseIP(remoteHost(r.RemoteAddr))
	if remote == nil {
		return "unknown"
	}
	if !isTrusted(remote, trusted) {
		return remote.String()
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	current := remote
	for i := len(forwarded) - 1; i >= 0; i-- {
		if !isTrusted(current, trusted) {
			break
		}
		candidate := net.ParseIP(strings.TrimSpace(forwarded[i]))
		if candidate == nil {
			return remote.String()
		}
		current = candidate
	}
	return current.String()
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return strings.Trim(remoteAddr, "[]")
}

func isTrusted(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

type errorBody struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeLimitError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Success: false, Error: code, Message: message})
}
