package internal

import (
	"time"

	"github.com/google/uuid"
)

// User represents an identity registered under a specific tenant.
type User struct {
	ID           uuid.UUID `json:"user_id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Role represents a tenant-scoped permission role.
type Role struct {
	ID       uuid.UUID `json:"role_id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Name     string    `json:"name"`
}

// RefreshToken represents a stateful rotated session token.
type RefreshToken struct {
	ID        uuid.UUID `json:"token_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	UserID    uuid.UUID `json:"user_id"`
	TokenHash string    `json:"-"`
	FamilyID  uuid.UUID `json:"family_id"`
	IsRevoked bool      `json:"is_revoked"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// TokenPair is returned upon successful authentication or token refresh.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}
