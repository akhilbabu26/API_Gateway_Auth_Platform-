package internal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"auth/internal/mail"
	"auth/internal/otp"
	"platform/hasher"
	"platform/jwt"

	"github.com/google/uuid"
)

var (
	ErrInvalidEmail        = errors.New("invalid email address")
	ErrPasswordTooShort    = errors.New("password must be at least 8 characters long")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrAccountSuspended    = errors.New("user account is suspended")
	ErrUnverifiedAccount   = errors.New("account pending verification: please verify your email first")
	ErrAlreadyVerified     = errors.New("email is already verified")
	ErrTokenReplayDetected = errors.New("token replay detected: refresh token has already been used")
	ErrRefreshTokenExpired = errors.New("refresh token has expired")
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type Service struct {
	repo         Repository
	hasher       *hasher.PasswordHasher
	tokenManager *jwt.TokenManager
	otpMgr       *otp.Manager
	mailer       mail.Mailer
}

func NewService(repo Repository, hasher *hasher.PasswordHasher, tokenManager *jwt.TokenManager, otpMgr *otp.Manager, mailer mail.Mailer) *Service {
	return &Service{
		repo:         repo,
		hasher:       hasher,
		tokenManager: tokenManager,
		otpMgr:       otpMgr,
		mailer:       mailer,
	}
}

// Register creates a new user in PENDING_VERIFICATION and dispatches a 6-digit OTP.
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

	// 1. Create user in PostgreSQL with status PENDING_VERIFICATION
	user, err := s.repo.CreateUser(ctx, tenantID, email, passwordHash)
	if err != nil {
		return nil, err
	}

	// 2. Generate 6-digit OTP in Redis with 5m TTL
	otpKey := fmt.Sprintf("%s:register:%s", tenantID, email)
	code, err := s.otpMgr.Generate(ctx, otpKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate verification otp: %w", err)
	}

	// 3. Dispatch simulated email
	_ = s.mailer.SendVerificationCode(ctx, email, code)

	return user, nil
}

// VerifyEmail verifies the OTP, marks user ACTIVE, and issues initial TokenPair.
func (s *Service) VerifyEmail(ctx context.Context, tenantID uuid.UUID, email, code string) (*TokenPair, error) {
	email = strings.TrimSpace(strings.ToLower(email))

	// 1. Verify OTP in Redis
	otpKey := fmt.Sprintf("%s:register:%s", tenantID, email)
	ok, err := s.otpMgr.Verify(ctx, otpKey, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("invalid verification code")
	}

	// 2. Update user status in DB to ACTIVE
	if err := s.repo.VerifyUserEmail(ctx, tenantID, email); err != nil {
		return nil, fmt.Errorf("failed to activate user: %w", err)
	}

	// 3. Look up user
	user, err := s.repo.GetUserByEmail(ctx, tenantID, email)
	if err != nil {
		return nil, fmt.Errorf("failed to load user: %w", err)
	}

	// 4. Assign default role
	_ = s.repo.AssignDefaultRole(ctx, tenantID, user.ID, "member")
	roles, _ := s.repo.GetUserRoles(ctx, tenantID, user.ID)

	// 5. Mint tokens
	accessToken, err := s.tokenManager.GenerateAccessToken(user.ID, user.TenantID, user.Email, roles)
	if err != nil {
		return nil, fmt.Errorf("failed to mint access token: %w", err)
	}

	rawRefreshToken, tokenHash, expiresAt, err := s.tokenManager.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to mint refresh token: %w", err)
	}

	refreshTokenRecord := &RefreshToken{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    user.ID,
		TokenHash: tokenHash,
		FamilyID:  uuid.New(),
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

// ResendVerificationCode re-generates and re-dispatches OTP for pending registration.
func (s *Service) ResendVerificationCode(ctx context.Context, tenantID uuid.UUID, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))

	user, err := s.repo.GetUserByEmail(ctx, tenantID, email)
	if err != nil {
		// Anti-enumeration: silently return success if user does not exist
		return nil
	}

	if user.Status == "ACTIVE" {
		return ErrAlreadyVerified
	}

	otpKey := fmt.Sprintf("%s:register:%s", tenantID, email)
	code, err := s.otpMgr.Generate(ctx, otpKey)
	if err != nil {
		return fmt.Errorf("failed to regenerate otp: %w", err)
	}

	return s.mailer.SendVerificationCode(ctx, email, code)
}

// ForgotPassword requests a password reset OTP.
func (s *Service) ForgotPassword(ctx context.Context, tenantID uuid.UUID, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))

	user, err := s.repo.GetUserByEmail(ctx, tenantID, email)
	if err != nil || user.Status != "ACTIVE" {
		// Anti-enumeration: silently return success
		return nil
	}

	otpKey := fmt.Sprintf("%s:reset:%s", tenantID, email)
	code, err := s.otpMgr.Generate(ctx, otpKey)
	if err != nil {
		return fmt.Errorf("failed to generate reset otp: %w", err)
	}

	return s.mailer.SendPasswordResetCode(ctx, email, code)
}

// ResetPassword verifies reset OTP, updates password, and revokes all active sessions.
func (s *Service) ResetPassword(ctx context.Context, tenantID uuid.UUID, email, code, newPassword string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	// 1. Verify OTP in Redis
	otpKey := fmt.Sprintf("%s:reset:%s", tenantID, email)
	ok, err := s.otpMgr.Verify(ctx, otpKey, code)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("invalid reset code")
	}

	// 2. Hash new password with Argon2id
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// 3. Atomically update password in DB and increment token_version
	if err := s.repo.UpdateUserPassword(ctx, tenantID, email, newHash); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	// 4. Global Session Invalidation: revoke all existing refresh tokens
	user, _ := s.repo.GetUserByEmail(ctx, tenantID, email)
	if user != nil {
		_ = s.repo.RevokeAllUserTokens(ctx, tenantID, user.ID)
	}

	return nil
}

// Login authenticates verified users and returns a TokenPair.
func (s *Service) Login(ctx context.Context, tenantID uuid.UUID, email, password string) (*TokenPair, error) {
	email = strings.TrimSpace(strings.ToLower(email))

	user, err := s.repo.GetUserByEmail(ctx, tenantID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if user.Status == "PENDING_VERIFICATION" {
		return nil, ErrUnverifiedAccount
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
