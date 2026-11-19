package jwt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("jwt: invalid or tampered token")
	ErrExpiredToken = errors.New("jwt: token has expired")
)

// Claims contains the token payload with multi-tenant identity and RBAC roles.
type Claims struct {
	UserID   uuid.UUID `json:"sub"`
	TenantID uuid.UUID `json:"tid"`
	Email    string    `json:"email"`
	Roles    []string  `json:"roles"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies JWT access tokens and opaque refresh tokens.
type TokenManager struct {
	secretKey  []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenManager creates a TokenManager.
func NewTokenManager(secretKey string, issuer string, accessTTL, refreshTTL time.Duration) *TokenManager {
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	if issuer == "" {
		issuer = "apigateway-auth-platform"
	}

	return &TokenManager{
		secretKey:  []byte(secretKey),
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// GenerateAccessToken mints a stateless JWT containing the user identity, tenant boundary, and assigned roles.
func (tm *TokenManager) GenerateAccessToken(userID, tenantID uuid.UUID, email string, roles []string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		TenantID: tenantID,
		Email:    email,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    tm.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.accessTTL)),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(tm.secretKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign access token: %w", err)
	}

	return signedToken, nil
}

// ValidateAccessToken parses and validates a signed JWT string in-memory.
func (tm *TokenManager) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return tm.secretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// GenerateRefreshToken generates a secure 32-byte string and its SHA-256 hex hash.
func (tm *TokenManager) GenerateRefreshToken() (rawToken string, tokenHash string, expiresAt time.Time, err error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to generate random bytes: %w", err)
	}

	rawToken = base64.RawURLEncoding.EncodeToString(randomBytes)
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(hash[:])
	expiresAt = time.Now().Add(tm.refreshTTL)

	return rawToken, tokenHash, expiresAt, nil
}

// HashRefreshToken calculates the SHA-256 hex string of a raw refresh token.
func (tm *TokenManager) HashRefreshToken(rawToken string) string {
	hash := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(hash[:])
}

// AccessTokenTTL returns access token duration in seconds.
func (tm *TokenManager) AccessTokenTTL() int64 {
	return int64(tm.accessTTL.Seconds())
}
