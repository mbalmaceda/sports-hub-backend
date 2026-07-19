package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `
	id, name, email,
	COALESCE(phone,''), COALESCE(avatar_url,''),
	COALESCE(push_token,''), password_hash,
	created_at, updated_at`

func scanUser(row pgx.Row) (*user.User, error) {
	u := &user.User{}
	err := row.Scan(
		&u.ID, &u.Name, &u.Email,
		&u.Phone, &u.AvatarURL,
		&u.PushToken, &u.PasswordHash,
		&u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (*user.User, error) {
	q := `SELECT` + userColumns + ` FROM users WHERE id = $1`
	u, err := scanUser(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user.FindByID: %w", err)
	}
	return u, nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	q := `SELECT` + userColumns + ` FROM users WHERE email = $1`
	u, err := scanUser(r.pool.QueryRow(ctx, q, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user.FindByEmail: %w", err)
	}
	return u, nil
}

func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	const q = `
		INSERT INTO users (name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`
	err := r.pool.QueryRow(ctx, q, u.Name, u.Email, u.PasswordHash).
		Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("user.Create: %w", err)
	}
	return nil
}

// UpdateProfile actualiza solo los campos enviados. Campos vacíos no sobreescriben el valor existente.
func (r *UserRepository) UpdateProfile(ctx context.Context, userID string, upd user.ProfileUpdate) error {
	const q = `
		UPDATE users SET
			name       = CASE WHEN $1 != '' THEN $1 ELSE name END,
			phone      = CASE WHEN $2 != '' THEN $2 ELSE phone END,
			avatar_url = CASE WHEN $3 != '' THEN $3 ELSE avatar_url END,
			updated_at = NOW()
		WHERE id = $4`
	_, err := r.pool.Exec(ctx, q, upd.Name, upd.Phone, upd.AvatarURL, userID)
	if err != nil {
		return fmt.Errorf("user.UpdateProfile: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdatePushToken(ctx context.Context, userID, token string) error {
	const q = `UPDATE users SET push_token = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.pool.Exec(ctx, q, token, userID)
	if err != nil {
		return fmt.Errorf("user.UpdatePushToken: %w", err)
	}
	return nil
}
