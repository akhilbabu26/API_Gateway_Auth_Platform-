package jwt

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTokenManager(t *testing.T) {
	tm := NewTokenManager("super-secret-key-32-bytes-minimum-jwt", "test-issuer", 15*time.Minute, 7*24*time.Hour)

	userID := uuid.New()
	tenantID := uuid.New()
	email := "user@tradedrift-style.com"
	roles := []string{"admin", "member"}

	tokenStr, err := tm.GenerateAccessToken(userID, tenantID, email, roles)
	if err != nil {
		t.Fatalf("failed to generate access token: %v", err)
	}

	claims, err := tm.ValidateAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}

	if claims.UserID != userID || claims.TenantID != tenantID || claims.Email != email {
		t.Fatalf("claims mismatch: got %+v", claims)
	}

	rawRefresh, tokenHash, expiresAt, err := tm.GenerateRefreshToken()
	if err != nil || rawRefresh == "" || tokenHash == "" {
		t.Fatalf("failed to generate refresh token: %v", err)
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expected refresh token expiration in future")
	}

	if tm.HashRefreshToken(rawRefresh) != tokenHash {
		t.Fatal("hash verification mismatch")
	}
}
