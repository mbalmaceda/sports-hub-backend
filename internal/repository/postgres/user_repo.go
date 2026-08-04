package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	COALESCE(tax_id,''), COALESCE(phone,''), COALESCE(avatar_url,''),
	COALESCE(favorite_sport,''), height_cm, weight_kg, birth_date,
	COALESCE(alias,''), COALESCE(city,''), COALESCE(dominant_side,''), COALESCE(bio,''),
	COALESCE(push_token,''), password_hash,
	created_at, updated_at`

func scanUser(row pgx.Row) (*user.User, error) {
	u := &user.User{}
	err := row.Scan(
		&u.ID, &u.Name, &u.Email,
		&u.TaxID, &u.Phone, &u.AvatarURL,
		&u.FavoriteSport, &u.HeightCm, &u.WeightKg, &u.BirthDate,
		&u.Alias, &u.City, &u.DominantSide, &u.Bio,
		&u.PushToken, &u.PasswordHash,
		&u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

func isTaxIDConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "users_tax_id_unique"
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (*user.User, error) {
	q := `SELECT` + userColumns + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
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
	q := `SELECT` + userColumns + ` FROM users WHERE email = $1 AND deleted_at IS NULL`
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
		INSERT INTO users (
			name, email, password_hash, tax_id, favorite_sport,
			height_cm, weight_kg, birth_date, alias, city, dominant_side, bio
		)
		VALUES (
			$1, $2, $3, NULLIF($4, ''), NULLIF($5, ''),
			$6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, '')
		)
		RETURNING id, created_at, updated_at`
	err := r.pool.QueryRow(ctx, q,
		u.Name, u.Email, u.PasswordHash, u.TaxID, u.FavoriteSport,
		u.HeightCm, u.WeightKg, u.BirthDate, u.Alias, u.City, u.DominantSide, u.Bio,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isTaxIDConflict(err) {
			return user.ErrTaxIDTaken
		}
		return fmt.Errorf("user.Create: %w", err)
	}
	return nil
}

// UpdateProfile actualiza solo los campos enviados. Campos vacíos no sobreescriben el valor existente.
func (r *UserRepository) UpdateProfile(ctx context.Context, userID string, upd user.ProfileUpdate) error {
	const q = `
		UPDATE users SET
			name           = CASE WHEN $1  != '' THEN $1  ELSE name END,
			tax_id         = CASE WHEN $2  != '' THEN $2  ELSE tax_id END,
			phone          = CASE WHEN $3  != '' THEN $3  ELSE phone END,
			avatar_url     = CASE WHEN $4  != '' THEN $4  ELSE avatar_url END,
			favorite_sport = CASE WHEN $5  != '' THEN $5  ELSE favorite_sport END,
			height_cm      = COALESCE($6, height_cm),
			weight_kg      = COALESCE($7, weight_kg),
			birth_date     = COALESCE($8, birth_date),
			alias          = CASE WHEN $9  != '' THEN $9  ELSE alias END,
			city           = CASE WHEN $10 != '' THEN $10 ELSE city END,
			dominant_side  = CASE WHEN $11 != '' THEN $11 ELSE dominant_side END,
			bio            = CASE WHEN $12 != '' THEN $12 ELSE bio END,
			updated_at     = NOW()
		WHERE id = $13`
	_, err := r.pool.Exec(ctx, q,
		upd.Name, upd.TaxID, upd.Phone, upd.AvatarURL, upd.FavoriteSport,
		upd.HeightCm, upd.WeightKg, upd.BirthDate,
		upd.Alias, upd.City, upd.DominantSide, upd.Bio, userID,
	)
	if err != nil {
		if isTaxIDConflict(err) {
			return user.ErrTaxIDTaken
		}
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

// Delete anonimiza la cuenta en vez de borrar la fila (ver migración 014) y de
// paso corta las sesiones abiertas. Va todo en una transacción: una cuenta a
// medio borrar es peor que una no borrada.
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("user.Delete: %w", err)
	}
	defer tx.Rollback(ctx)

	// El email se reescribe a uno irrecuperable en vez de vaciarse porque la
	// columna es UNIQUE y NOT NULL, y porque hay que liberar el correo original:
	// quien borró su cuenta tiene derecho a volver a registrarse con el mismo.
	// `.invalid` es el TLD que RFC 2606 reserva justo para esto, así que la
	// dirección resultante no le puede llegar a nadie.
	const anonymize = `
		UPDATE users SET
			name           = 'Cuenta eliminada',
			email          = 'deleted-' || id || '@zports.invalid',
			password_hash  = '',
			tax_id         = NULL,
			phone          = NULL,
			avatar_url     = NULL,
			favorite_sport = NULL,
			height_cm      = NULL,
			weight_kg      = NULL,
			birth_date     = NULL,
			alias          = NULL,
			city           = NULL,
			dominant_side  = NULL,
			bio            = NULL,
			push_token     = NULL,
			deleted_at     = NOW(),
			updated_at     = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
	tag, err := tx.Exec(ctx, anonymize, id)
	if err != nil {
		return fmt.Errorf("user.Delete: %w", err)
	}
	// Cero filas es la cuenta que no existe o que ya se borró; en ambos casos no
	// hay nada que hacer y quien llama necesita distinguirlo de un borrado real.
	if tag.RowsAffected() == 0 {
		return user.ErrNotFound
	}

	// Sin esto la sesión sobrevive al borrado: el refresh token dura días.
	if _, err := tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, id); err != nil {
		return fmt.Errorf("user.Delete refresh_tokens: %w", err)
	}

	// La membresía no se borra: de ella cuelgan las cuotas y los cobros ya
	// emitidos. Queda inactiva, que es como el equipo ve a quien ya no está.
	if _, err := tx.Exec(ctx, `UPDATE memberships SET status = 'inactive' WHERE user_id = $1`, id); err != nil {
		return fmt.Errorf("user.Delete memberships: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("user.Delete commit: %w", err)
	}
	return nil
}
