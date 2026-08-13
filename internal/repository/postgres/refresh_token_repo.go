package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// Create inserta un eslabón. Si FamilyID viene vacío es un login nuevo y la
// base genera la familia; si viene, el token hereda la cadena del que rotó.
func (r *RefreshTokenRepository) Create(ctx context.Context, t *auth.RefreshToken) error {
	const q = `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, COALESCE($2::uuid, gen_random_uuid()), $3, $4)
		RETURNING id, family_id, created_at`
	var familyID *string
	if t.FamilyID != "" {
		familyID = &t.FamilyID
	}
	err := r.pool.QueryRow(ctx, q, t.UserID, familyID, t.TokenHash, t.ExpiresAt).
		Scan(&t.ID, &t.FamilyID, &t.CreatedAt)
	if err != nil {
		return fmt.Errorf("refresh_token.Create: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	const q = `
		SELECT id, user_id, family_id, token_hash, expires_at, created_at, used_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`
	t := &auth.RefreshToken{}
	err := r.pool.QueryRow(ctx, q, tokenHash).
		Scan(&t.ID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt, &t.UsedAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("refresh_token.FindByHash: %w", err)
	}
	return t, nil
}

// MarkUsed marca la rotación. El `used_at IS NULL` del WHERE es lo que resuelve
// la carrera entre dos refresh simultáneos: el UPDATE es atómico, así que solo
// uno afecta una fila y solo ese emite el token nuevo. El otro recibe
// ErrTokenNotFound y cae en el camino de gracia.
func (r *RefreshTokenRepository) MarkUsed(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE refresh_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, id, at)
	if err != nil {
		return fmt.Errorf("refresh_token.MarkUsed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrTokenNotFound
	}
	return nil
}

func (r *RefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string, at time.Time) error {
	const q = `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, familyID, at); err != nil {
		return fmt.Errorf("refresh_token.RevokeFamily: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string, at time.Time) error {
	const q = `UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, userID, at); err != nil {
		return fmt.Errorf("refresh_token.RevokeAllForUser: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM refresh_tokens WHERE expires_at < $1`
	tag, err := r.pool.Exec(ctx, q, before)
	if err != nil {
		return 0, fmt.Errorf("refresh_token.DeleteExpired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TrimFamilies borra las cadenas más viejas del usuario, dejando las `keep`
// últimas. Se ordena por el eslabón más reciente de cada familia y no por su
// creación: una sesión de hace meses que se sigue usando está más viva que un
// login de ayer que quedó abandonado.
func (r *RefreshTokenRepository) TrimFamilies(ctx context.Context, userID string, keep int) error {
	const q = `
		DELETE FROM refresh_tokens
		WHERE user_id = $1
		  AND family_id NOT IN (
			SELECT family_id FROM refresh_tokens
			WHERE user_id = $1
			GROUP BY family_id
			ORDER BY max(created_at) DESC
			LIMIT $2
		  )`
	if _, err := r.pool.Exec(ctx, q, userID, keep); err != nil {
		return fmt.Errorf("refresh_token.TrimFamilies: %w", err)
	}
	return nil
}
