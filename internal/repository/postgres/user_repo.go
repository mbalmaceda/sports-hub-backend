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

func (r *UserRepository) FindByID(ctx context.Context, id string) (*user.User, error) {
	const q = `
		SELECT id, name, email, role, COALESCE(push_token, ''), created_at, updated_at
		FROM users WHERE id = $1`

	u := &user.User{}
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.PushToken, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user.FindByID: %w", err)
	}
	return u, nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	const q = `
		SELECT id, name, email, role, COALESCE(push_token, ''), created_at, updated_at
		FROM users WHERE email = $1`

	u := &user.User{}
	err := r.pool.QueryRow(ctx, q, email).
		Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.PushToken, &u.CreatedAt, &u.UpdatedAt)
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
		INSERT INTO users (name, email, role)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`

	err := r.pool.QueryRow(ctx, q, u.Name, u.Email, u.Role).
		Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("user.Create: %w", err)
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
