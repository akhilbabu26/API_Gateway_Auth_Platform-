package internal

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound         = errors.New("repository: user not found")
	ErrUserAlreadyExists    = errors.New("repository: user with this email already exists in tenant")
	ErrRefreshTokenNotFound = errors.New("repository: refresh token not found")
	ErrTenantNotFound       = errors.New("repository: tenant not found")
)

// Repository defines database persistence operations for auth.
type Repository interface {
	TenantExists(ctx context.Context, tenantID uuid.UUID) (bool, error)
	CreateUser(ctx context.Context, tenantID uuid.UUID, email, passwordHash string) (*User, error)
	GetUserByEmail(ctx context.Context, tenantID uuid.UUID, email string) (*User, error)
	GetUserRoles(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error)
	AssignDefaultRole(ctx context.Context, tenantID, userID uuid.UUID, roleName string) error

	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error
	RevokeTokenFamily(ctx context.Context, familyID uuid.UUID) error
}
