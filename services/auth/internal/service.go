package internal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"platform/hasher"
	"platform/jwt"

	"github.com/google/uuid"
)

var (
	ErrInvalidEmail        = errors.New("invalid email address")
	ErrPasswordTooShort    = errors.New("password must be at least 8 characters long")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrAccountSuspended    = errors.New("user account is suspended")
	ErrTokenReplayDetected = errors.New("token replay detected: refresh token has already been used")
	ErrRefreshTokenExpired = errors.New("refresh token has expired")
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type Service struct {
	repo         Repository
	hasher       *hasher.PasswordHasher
	tokenManager *jwt.TokenManager
}

func NewService(repo Repository, hasher *hasher.PasswordHasher, tokenManager *jwt.TokenManager) *Service {
	return &Service{
		repo:         repo,
		hasher:       hasher,
		tokenManager: tokenManager,
	}
}

func (s *Service) Register(ctx context.Context, tenantID uuid.UUID, email, password string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if !emailRegex.MatchString(email) {
		return nil, ErrInvalidEmail
	}
	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	exists, err := s.repo.TenantExists(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify tenant: %w", err)
	}
	if !exists {
		return nil, ErrTenantNotFound
	}

	passwordHash, err := s.hasher.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user, err := s.repo.CreateUser(ctx, tenantID, email, passwordHash)
	if err != nil {
		return nil, err
	}

	_ = s.repo.AssignDefaultRole(ctx, tenantID, user.ID, "member")

	return user, nil
}

func (s *Service) Login(ctx context.Context, tenantID uuid.UUID, email, password string) (*TokenPair, error) {
	email = strings.TrimSpace(strings.ToLower(email))

	user, err := s.repo.GetUserByEmail(ctx, tenantID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if user.Status == "SUSPENDED" {
		return nil, ErrAccountSuspended
	}

	match, err := s.hasher.VerifyPassword(password, user.PasswordHash)
	if err != nil || !match {
		return nil, ErrInvalidCredentials
	}

	roles, err := s.repo.GetUserRoles(ctx, tenantID, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user roles: %w", err)
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(user.ID, user.TenantID, user.Email, roles)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	rawRefreshToken, tokenHash, expiresAt, err := s.tokenManager.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	familyID := uuid.New()
	refreshTokenRecord := &RefreshToken{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    user.ID,
		TokenHash: tokenHash,
		FamilyID:  familyID,
		IsRevoked: false,
		ExpiresAt: expiresAt,
	}

	if err := s.repo.CreateRefreshToken(ctx, refreshTokenRecord); err != nil {
		return nil, fmt.Errorf("failed to persist refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    s.tokenManager.AccessTokenTTL(),
	}, nil
}

func (s *Service) RefreshToken(ctx context.Context, tenantID uuid.UUID, rawRefreshToken string) (*TokenPair, error) {
	tokenHash := s.tokenManager.HashRefreshToken(rawRefreshToken)

	tokenRecord, err := s.repo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to query refresh token: %w", err)
	}

	if tokenRecord.TenantID != tenantID {
		return nil, ErrInvalidCredentials
	}

	if tokenRecord.IsRevoked {
		_ = s.repo.RevokeTokenFamily(ctx, tokenRecord.FamilyID)
		return nil, ErrTokenReplayDetected
	}

	if time.Now().After(tokenRecord.ExpiresAt) {
		return nil, ErrRefreshTokenExpired
	}

	if err := s.repo.RevokeRefreshToken(ctx, tokenRecord.ID); err != nil {
		return nil, fmt.Errorf("failed to revoke used refresh token: %w", err)
	}

	roles, _ := s.repo.GetUserRoles(ctx, tenantID, tokenRecord.UserID)

	newAccessToken, err := s.tokenManager.GenerateAccessToken(tokenRecord.UserID, tokenRecord.TenantID, "", roles)
	if err != nil {
		return nil, fmt.Errorf("failed to mint new access token: %w", err)
	}

	newRawRefreshToken, newTokenHash, newExpiresAt, err := s.tokenManager.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to mint new refresh token: %w", err)
	}

	newRefreshTokenRecord := &RefreshToken{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    tokenRecord.UserID,
		TokenHash: newTokenHash,
		FamilyID:  tokenRecord.FamilyID,
		IsRevoked: false,
		ExpiresAt: newExpiresAt,
	}

	if err := s.repo.CreateRefreshToken(ctx, newRefreshTokenRecord); err != nil {
		return nil, fmt.Errorf("failed to store new refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  newAccessToken,
		RefreshToken: newRawRefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    s.tokenManager.AccessTokenTTL(),
	}, nil
}

func (s *Service) Logout(ctx context.Context, rawRefreshToken string) error {
	tokenHash := s.tokenManager.HashRefreshToken(rawRefreshToken)
	tokenRecord, err := s.repo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil
		}
		return err
	}

	return s.repo.RevokeRefreshToken(ctx, tokenRecord.ID)
}
