package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) TenantExists(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM tenants WHERE tenant_id = $1 AND status = 'ACTIVE')`
	var exists bool
	err := r.pool.QueryRow(ctx, query, tenantID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("postgres: error checking tenant existence: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) CreateUser(ctx context.Context, tenantID uuid.UUID, email, passwordHash string) (*User, error) {
	query := `
		INSERT INTO users (tenant_id, email, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'ACTIVE', NOW(), NOW())
		RETURNING user_id, tenant_id, email, status, created_at, updated_at
	`
	user := &User{}
	err := r.pool.QueryRow(ctx, query, tenantID, email, passwordHash).Scan(
		&user.ID,
		&user.TenantID,
		&user.Email,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrUserAlreadyExists
		}
		return nil, fmt.Errorf("postgres: failed to insert user: %w", err)
	}

	return user, nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, tenantID uuid.UUID, email string) (*User, error) {
	query := `
		SELECT user_id, tenant_id, email, password_hash, status, created_at, updated_at
		FROM users
		WHERE tenant_id = $1 AND email = $2
	`
	user := &User{}
	err := r.pool.QueryRow(ctx, query, tenantID, email).Scan(
		&user.ID,
		&user.TenantID,
		&user.Email,
		&user.PasswordHash,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("postgres: failed to query user: %w", err)
	}

	return user, nil
}

func (r *PostgresRepository) GetUserRoles(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
	query := `
		SELECT r.name
		FROM roles r
		INNER JOIN user_roles ur ON ur.role_id = r.role_id
		WHERE r.tenant_id = $1 AND ur.user_id = $2
	`
	rows, err := r.pool.Query(ctx, query, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to query user roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var roleName string
		if err := rows.Scan(&roleName); err != nil {
			return nil, err
		}
		roles = append(roles, roleName)
	}

	return roles, nil
}

func (r *PostgresRepository) AssignDefaultRole(ctx context.Context, tenantID, userID uuid.UUID, roleName string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var roleID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO roles (tenant_id, name)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id, name) DO UPDATE SET name = EXCLUDED.name
		RETURNING role_id
	`, tenantID, roleName).Scan(&roleID)
	if err != nil {
		return fmt.Errorf("postgres: failed to upsert default role: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, roleID)
	if err != nil {
		return fmt.Errorf("postgres: failed to link user to role: %w", err)
	}

	return tx.Commit(ctx)
}

func (r *PostgresRepository) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (token_id, tenant_id, user_id, token_hash, family_id, is_revoked, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`
	_, err := r.pool.Exec(ctx, query,
		token.ID,
		token.TenantID,
		token.UserID,
		token.TokenHash,
		token.FamilyID,
		token.IsRevoked,
		token.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: failed to insert refresh token: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	query := `
		SELECT token_id, tenant_id, user_id, token_hash, family_id, is_revoked, expires_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1
	`
	token := &RefreshToken{}
	err := r.pool.QueryRow(ctx, query, tokenHash).Scan(
		&token.ID,
		&token.TenantID,
		&token.UserID,
		&token.TokenHash,
		&token.FamilyID,
		&token.IsRevoked,
		&token.ExpiresAt,
		&token.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("postgres: failed to query refresh token: %w", err)
	}

	return token, nil
}

func (r *PostgresRepository) RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error {
	query := `UPDATE refresh_tokens SET is_revoked = TRUE WHERE token_id = $1`
	_, err := r.pool.Exec(ctx, query, tokenID)
	if err != nil {
		return fmt.Errorf("postgres: failed to revoke token: %w", err)
	}
	return nil
}

func (r *PostgresRepository) RevokeTokenFamily(ctx context.Context, familyID uuid.UUID) error {
	query := `UPDATE refresh_tokens SET is_revoked = TRUE WHERE family_id = $1`
	_, err := r.pool.Exec(ctx, query, familyID)
	if err != nil {
		return fmt.Errorf("postgres: failed to revoke token family: %w", err)
	}
	return nil
}
