package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
)

type GuestInviteRepository struct {
	pool *pgxpool.Pool
}

func NewGuestInviteRepository(pool *pgxpool.Pool) *GuestInviteRepository {
	return &GuestInviteRepository{pool: pool}
}

const guestInviteColumns = `
	id, match_id, team_id, created_by_user_id,
	max_uses, used_count, expires_at, revoked_at, created_at`

func scanInvite(row pgx.Row) (*guest.Invite, error) {
	i := &guest.Invite{}
	err := row.Scan(
		&i.ID, &i.MatchID, &i.TeamID, &i.CreatedByUserID,
		&i.MaxUses, &i.UsedCount, &i.ExpiresAt, &i.RevokedAt, &i.CreatedAt,
	)
	return i, err
}

func (r *GuestInviteRepository) Create(ctx context.Context, inv *guest.Invite, tokenHash string) error {
	const q = `
		INSERT INTO match_guest_invites
			(token_hash, match_id, team_id, created_by_user_id, max_uses, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, used_count, created_at`
	err := r.pool.QueryRow(ctx, q,
		tokenHash, inv.MatchID, inv.TeamID, inv.CreatedByUserID, inv.MaxUses, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.UsedCount, &inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("guest.Create: %w", err)
	}
	return nil
}

func (r *GuestInviteRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*guest.Invite, error) {
	q := `SELECT` + guestInviteColumns + ` FROM match_guest_invites WHERE token_hash = $1`
	i, err := scanInvite(r.pool.QueryRow(ctx, q, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, guest.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("guest.FindByTokenHash: %w", err)
	}
	return i, nil
}

func (r *GuestInviteRepository) FindByID(ctx context.Context, id string) (*guest.Invite, error) {
	q := `SELECT` + guestInviteColumns + ` FROM match_guest_invites WHERE id = $1`
	i, err := scanInvite(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, guest.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("guest.FindByID: %w", err)
	}
	return i, nil
}

func (r *GuestInviteRepository) ListByMatch(ctx context.Context, matchID string) ([]*guest.Invite, error) {
	q := `SELECT` + guestInviteColumns + `
		FROM match_guest_invites WHERE match_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, matchID)
	if err != nil {
		return nil, fmt.Errorf("guest.ListByMatch: %w", err)
	}
	defer rows.Close()

	invites, err := collect(rows, scanInvite)
	if err != nil {
		return nil, fmt.Errorf("guest.ListByMatch scan: %w", err)
	}
	return invites, nil
}

func (r *GuestInviteRepository) Revoke(ctx context.Context, id string, at time.Time) error {
	// Solo el primer revoke cuenta: revocar dos veces no mueve la fecha, que es
	// cuándo se apagó de verdad.
	const q = `UPDATE match_guest_invites SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, at, id); err != nil {
		return fmt.Errorf("guest.Revoke: %w", err)
	}
	return nil
}

/*
Redeem canjea el enlace: descuenta el cupo, da de alta la membresía de invitado
y deja la convocatoria confirmada, todo en una transacción.

El orden importa y el primer paso es el que sostiene lo demás. El UPDATE del
cupo lleva las tres condiciones de validez en el WHERE —vigente, no revocado,
con lugar— así que es a la vez el chequeo y la reserva: dos personas tocando el
enlace al mismo tiempo se serializan en esa fila y la segunda no encuentra cupo.
Chequear antes y descontar después dejaba la ventana justo en el medio, que es
donde entran los dos que abren el link a la vez cuando el manager lo acaba de
mandar al grupo.

La convocatoria nace 'confirmed' y no 'called': el que canjea el enlace ya dijo
que va —por eso lo canjeó—, y volver a preguntarle sería no haber entendido lo
que acaba de hacer.
*/
func (r *GuestInviteRepository) Redeem(
	ctx context.Context, tokenHash, userID string, now time.Time,
) (*guest.AcceptResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("guest.Redeem begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// El enlace primero, para saber a qué partido y equipo entra.
	var inviteID, matchID, teamID string
	const findQ = `
		SELECT id, match_id, team_id FROM match_guest_invites
		WHERE token_hash = $1`
	err = tx.QueryRow(ctx, findQ, tokenHash).Scan(&inviteID, &matchID, &teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, guest.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("guest.Redeem find: %w", err)
	}

	// Alguien del plantel que abre el enlace del grupo no gasta un lugar: ya
	// está adentro. Se le devuelve su propia membresía para que la app lo lleve
	// al partido igual, que es lo que quería hacer.
	var existingID string
	var existingKind string
	err = tx.QueryRow(ctx,
		`SELECT id, kind FROM memberships WHERE user_id = $1 AND team_id = $2`,
		userID, teamID,
	).Scan(&existingID, &existingKind)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("guest.Redeem existing: %w", err)
	}
	if err == nil && existingKind != string(membershipKindGuest) {
		return nil, guest.ErrAlreadyMember
	}

	membershipID := existingID

	// Consumir el cupo es lo primero que escribe, y es condicional: si no
	// quedaba lugar —o venció, o lo revocaron— no toca nada y se corta acá.
	// El invitado que vuelve a canjear el mismo enlace tampoco gasta cupo.
	if membershipID == "" {
		const consumeQ = `
			UPDATE match_guest_invites
			SET used_count = used_count + 1
			WHERE id = $1
			  AND revoked_at IS NULL
			  AND expires_at > $2
			  AND used_count < max_uses`
		tag, err := tx.Exec(ctx, consumeQ, inviteID, now)
		if err != nil {
			return nil, fmt.Errorf("guest.Redeem consume: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, guest.ErrNotUsable
		}

		// plays_as_player va en TRUE: el invitado ocupa un lugar en la nómina
		// de ese partido y paga su cuota de cancha como cualquiera. Lo que lo
		// distingue es kind, no que no juegue.
		const createQ = `
			INSERT INTO memberships (user_id, team_id, role, kind, plays_as_player, status)
			VALUES ($1, $2, 'player', 'guest', TRUE, 'active')
			RETURNING id`
		if err := tx.QueryRow(ctx, createQ, userID, teamID).Scan(&membershipID); err != nil {
			return nil, fmt.Errorf("guest.Redeem membership: %w", err)
		}
	}

	// Idempotente: canjear dos veces no resetea la respuesta ni duplica la fila.
	const callupQ = `
		INSERT INTO match_callups (match_id, membership_id, status, responded_at)
		VALUES ($1, $2, 'confirmed', $3)
		ON CONFLICT (match_id, membership_id) DO NOTHING`
	if _, err := tx.Exec(ctx, callupQ, matchID, membershipID, now); err != nil {
		return nil, fmt.Errorf("guest.Redeem callup: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("guest.Redeem commit: %w", err)
	}

	return &guest.AcceptResult{
		MembershipID: membershipID,
		TeamID:       teamID,
		MatchID:      matchID,
	}, nil
}

// membershipKindGuest evita importar el paquete de membresía solo por la
// constante y que este repositorio dependa de un dominio que no es el suyo.
const membershipKindGuest = "guest"
