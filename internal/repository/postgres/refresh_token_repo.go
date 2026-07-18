package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
)

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, t *auth.RefreshToken) error {
	const q = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q, t.UserID, t.TokenHash, t.ExpiresAt).
		Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return fmt.Errorf("refresh_token.Create: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	const q = `SELECT id, user_id, token_hash, expires_at, created_at FROM refresh_tokens WHERE token_hash = $1`
	t := &auth.RefreshToken{}
	err := r.pool.QueryRow(ctx, q, tokenHash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("refresh_token.FindByHash: %w", err)
	}
	return t, nil
}

func (r *RefreshTokenRepository) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE id = $1`, id)
	return err
}

func (r *RefreshTokenRepository) DeleteByHash(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE token_hash = $1`, tokenHash)
	return err
}
