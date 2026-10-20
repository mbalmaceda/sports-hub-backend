package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/teamlink"
)

type TeamLinkRepository struct {
	pool *pgxpool.Pool
}

func NewTeamLinkRepository(pool *pgxpool.Pool) *TeamLinkRepository {
	return &TeamLinkRepository{pool: pool}
}

const teamLinkColumns = `
	id, team_id, created_by_user_id,
	max_uses, used_count, expires_at, revoked_at, created_at`

func scanTeamLink(row pgx.Row) (*teamlink.Invite, error) {
	i := &teamlink.Invite{}
	err := row.Scan(
		&i.ID, &i.TeamID, &i.CreatedByUserID,
		&i.MaxUses, &i.UsedCount, &i.ExpiresAt, &i.RevokedAt, &i.CreatedAt,
	)
	return i, err
}

func (r *TeamLinkRepository) Create(ctx context.Context, inv *teamlink.Invite, tokenHash string) error {
	const q = `
		INSERT INTO team_invite_links (token_hash, team_id, created_by_user_id, max_uses, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, used_count, created_at`
	err := r.pool.QueryRow(ctx, q,
		tokenHash, inv.TeamID, inv.CreatedByUserID, inv.MaxUses, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.UsedCount, &inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("teamlink.Create: %w", err)
	}
	return nil
}

func (r *TeamLinkRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*teamlink.Invite, error) {
	q := `SELECT` + teamLinkColumns + ` FROM team_invite_links WHERE token_hash = $1`
	i, err := scanTeamLink(r.pool.QueryRow(ctx, q, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, teamlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("teamlink.FindByTokenHash: %w", err)
	}
	return i, nil
}

func (r *TeamLinkRepository) FindByID(ctx context.Context, id string) (*teamlink.Invite, error) {
	q := `SELECT` + teamLinkColumns + ` FROM team_invite_links WHERE id = $1`
	i, err := scanTeamLink(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, teamlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("teamlink.FindByID: %w", err)
	}
	return i, nil
}

func (r *TeamLinkRepository) ListByTeam(ctx context.Context, teamID string) ([]*teamlink.Invite, error) {
	q := `SELECT` + teamLinkColumns + `
		FROM team_invite_links WHERE team_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("teamlink.ListByTeam: %w", err)
	}
	defer rows.Close()

	invites, err := collect(rows, scanTeamLink)
	if err != nil {
		return nil, fmt.Errorf("teamlink.ListByTeam scan: %w", err)
	}
	return invites, nil
}

func (r *TeamLinkRepository) Revoke(ctx context.Context, id string, at time.Time) error {
	// Solo el primer revoke cuenta, igual que en el de los parches.
	const q = `UPDATE team_invite_links SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, at, id); err != nil {
		return fmt.Errorf("teamlink.Revoke: %w", err)
	}
	return nil
}

/*
Redeem canjea el enlace: descuenta el cupo y da de alta la membresía de
plantel, en una transacción.

Mismo orden que `GuestInviteRepository.Redeem`, por la misma razón: el UPDATE
del cupo lleva las tres condiciones de validez en el WHERE, así que es a la vez
el chequeo y la reserva, y dos personas que abren el enlace a la vez no pueden
pasarse del tope.

El alta es el mismo `ON CONFLICT` que aceptar una invitación por correo
(`OnboardingRepository.resolve`): el parche de este equipo no recibe una segunda
membresía, sube la que tenía —con la convocatoria y el cobro de aquel partido—,
y el que se había ido vuelve sobre su misma fila.
*/
func (r *TeamLinkRepository) Redeem(
	ctx context.Context, tokenHash, userID string, now time.Time,
) (*teamlink.AcceptResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("teamlink.Redeem begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var linkID, teamID string
	err = tx.QueryRow(ctx,
		`SELECT id, team_id FROM team_invite_links WHERE token_hash = $1`, tokenHash,
	).Scan(&linkID, &teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, teamlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("teamlink.Redeem find: %w", err)
	}

	/*
		¿Ya es del club? Activo o suspendido cuenta como adentro: el primero no
		tiene que gastar un lugar, y el segundo no puede reactivarse solo por
		tener el link —lo suspendió el manager—. El parche y el que se fue
		('inactive') siguen de largo y entran.
	*/
	var kind, status string
	err = tx.QueryRow(ctx,
		`SELECT kind, status FROM memberships WHERE user_id = $1 AND team_id = $2`,
		userID, teamID,
	).Scan(&kind, &status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("teamlink.Redeem existing: %w", err)
	}
	if err == nil && kind != membershipKindGuest && status != "inactive" {
		return nil, teamlink.ErrAlreadyMember
	}

	const consumeQ = `
		UPDATE team_invite_links
		SET used_count = used_count + 1
		WHERE id = $1
		  AND revoked_at IS NULL
		  AND expires_at > $2
		  AND used_count < max_uses`
	tag, err := tx.Exec(ctx, consumeQ, linkID, now)
	if err != nil {
		return nil, fmt.Errorf("teamlink.Redeem consume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, teamlink.ErrNotUsable
	}

	var membershipID string
	const addMember = `
		INSERT INTO memberships (user_id, team_id, role, status)
		VALUES ($1, $2, 'player', 'active')
		ON CONFLICT (user_id, team_id) DO UPDATE SET status = 'active', kind = 'member'
		RETURNING id`
	if err := tx.QueryRow(ctx, addMember, userID, teamID).Scan(&membershipID); err != nil {
		return nil, fmt.Errorf("teamlink.Redeem membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("teamlink.Redeem commit: %w", err)
	}
	return &teamlink.AcceptResult{MembershipID: membershipID, TeamID: teamID}, nil
}
