package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
)

type CompetitionRepository struct {
	pool *pgxpool.Pool
}

func NewCompetitionRepository(pool *pgxpool.Pool) *CompetitionRepository {
	return &CompetitionRepository{pool: pool}
}

const competitionColumns = `
	id, sport_id, type, name, organizer_team_id, status, start_at, end_at,
	venue, players_per_side, venue_cost_amount, venue_cost_currency, player_share,
	is_internal, created_at`

func scanCompetition(row pgx.Row) (*competition.Competition, error) {
	c := &competition.Competition{}
	var venue, currency *string
	var amount *int64

	err := row.Scan(
		&c.ID, &c.SportID, &c.Type, &c.Name, &c.OrganizerTeamID, &c.Status,
		&c.StartAt, &c.EndAt, &venue, &c.PlayersPerSide, &amount, &currency,
		&c.PlayerShare, &c.IsInternal, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if venue != nil {
		c.Venue = *venue
	}
	// El costo solo existe si están las dos partes: un monto sin moneda no se
	// puede formatear, y una moneda sin monto no dice nada.
	if amount != nil && currency != nil {
		c.VenueCost = &competition.VenueCost{Amount: *amount, Currency: *currency}
	}
	return c, nil
}

func (r *CompetitionRepository) FindByID(ctx context.Context, id string) (*competition.Competition, error) {
	q := `SELECT` + competitionColumns + ` FROM competitions WHERE id = $1`
	c, err := scanCompetition(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, competition.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("competition.FindByID: %w", err)
	}
	return c, nil
}

// ListByTeam devuelve las competencias donde el equipo es parte, aunque todavía
// no tenga una entrada. Un equipo retado en un amistoso, por ejemplo, no tiene
// entrada hasta que acepta: si la consulta solo mirara organizer/entries, la
// invitación no le llegaría a la bandeja.
func (r *CompetitionRepository) ListByTeam(ctx context.Context, teamID string) ([]*competition.Competition, error) {
	q := `SELECT` + competitionColumns + `
		FROM competitions c
		WHERE c.organizer_team_id = $1
		   OR EXISTS (SELECT 1 FROM competition_entries e
		              WHERE e.competition_id = c.id AND e.team_id = $1)
		   OR EXISTS (SELECT 1 FROM competition_invitations i
		              WHERE i.competition_id = c.id AND i.to_team_id = $1)
		   OR EXISTS (SELECT 1 FROM friendly_challenges fc
		              WHERE fc.competition_id = c.id
		                AND (fc.challenger_team_id = $1 OR fc.challenged_team_id = $1))
		ORDER BY c.created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("competition.ListByTeam: %w", err)
	}
	defer rows.Close()

	var result []*competition.Competition
	for rows.Next() {
		c, err := scanCompetition(rows)
		if err != nil {
			return nil, fmt.Errorf("competition.ListByTeam: scan: %w", err)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *CompetitionRepository) Create(ctx context.Context, c *competition.Competition) error {
	const q = `
		INSERT INTO competitions
			(sport_id, type, name, organizer_team_id, status, start_at, end_at,
			 venue, players_per_side, venue_cost_amount, venue_cost_currency, player_share,
			 is_internal)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at`

	var amount *int64
	var currency *string
	if c.VenueCost != nil {
		amount = &c.VenueCost.Amount
		currency = &c.VenueCost.Currency
	}

	// La cuota por jugador se resuelve acá y no en el handler: los amistosos y
	// los torneos se crean por caminos distintos y los dos pasan por este
	// método, así que es el único punto donde no se puede olvidar.
	c.PlayerShare = c.ResolvePlayerShare()

	err := r.pool.QueryRow(ctx, q,
		c.SportID, c.Type, c.Name, c.OrganizerTeamID, c.Status, c.StartAt, c.EndAt,
		nullIfEmpty(c.Venue), c.PlayersPerSide, amount, currency, c.PlayerShare,
		c.IsInternal,
	).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return fmt.Errorf("competition.Create: %w", err)
	}
	return nil
}

func (r *CompetitionRepository) UpdateStatus(ctx context.Context, id string, status competition.Status) error {
	const q = `UPDATE competitions SET status = $1 WHERE id = $2`
	if _, err := r.pool.Exec(ctx, q, status, id); err != nil {
		return fmt.Errorf("competition.UpdateStatus: %w", err)
	}
	return nil
}

func (r *CompetitionRepository) UpdateSchedule(
	ctx context.Context, id string, startAt time.Time, venue string,
) error {
	const q = `UPDATE competitions SET start_at = $1, venue = $2 WHERE id = $3`
	if _, err := r.pool.Exec(ctx, q, startAt, nullIfEmpty(venue), id); err != nil {
		return fmt.Errorf("competition.UpdateSchedule: %w", err)
	}
	return nil
}

// ─── Entradas ────────────────────────────────────────────────────────────────

func (r *CompetitionRepository) ListEntries(ctx context.Context, competitionID string) ([]*competition.Entry, error) {
	const q = `
		SELECT id, competition_id, team_id, status, joined_at
		FROM competition_entries
		WHERE competition_id = $1
		ORDER BY joined_at NULLS LAST`
	rows, err := r.pool.Query(ctx, q, competitionID)
	if err != nil {
		return nil, fmt.Errorf("competition.ListEntries: %w", err)
	}
	defer rows.Close()

	var result []*competition.Entry
	for rows.Next() {
		e := &competition.Entry{}
		if err := rows.Scan(&e.ID, &e.CompetitionID, &e.TeamID, &e.Status, &e.JoinedAt); err != nil {
			return nil, fmt.Errorf("competition.ListEntries: scan: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (r *CompetitionRepository) UpsertEntry(ctx context.Context, e *competition.Entry) error {
	const q = `
		INSERT INTO competition_entries (competition_id, team_id, status, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (competition_id, team_id) DO UPDATE
			SET status = EXCLUDED.status,
			    -- No se pisa la fecha de ingreso original si ya estaba adentro.
			    joined_at = COALESCE(competition_entries.joined_at, EXCLUDED.joined_at)
		RETURNING id, joined_at`
	err := r.pool.QueryRow(ctx, q, e.CompetitionID, e.TeamID, e.Status, e.JoinedAt).
		Scan(&e.ID, &e.JoinedAt)
	if err != nil {
		return fmt.Errorf("competition.UpsertEntry: %w", err)
	}
	return nil
}

// ─── Invitaciones ────────────────────────────────────────────────────────────

const invitationColumns = `
	id, competition_id, from_team_id, to_team_id, status, expires_at, created_at, responded_at`

func scanInvitation(row pgx.Row) (*competition.Invitation, error) {
	inv := &competition.Invitation{}
	err := row.Scan(
		&inv.ID, &inv.CompetitionID, &inv.FromTeamID, &inv.ToTeamID,
		&inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.RespondedAt,
	)
	return inv, err
}

func (r *CompetitionRepository) FindInvitation(ctx context.Context, id string) (*competition.Invitation, error) {
	q := `SELECT` + invitationColumns + ` FROM competition_invitations WHERE id = $1`
	inv, err := scanInvitation(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, competition.ErrInvitationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("competition.FindInvitation: %w", err)
	}
	return inv, nil
}

func (r *CompetitionRepository) ListInvitationsForTeam(ctx context.Context, teamID string) ([]*competition.Invitation, error) {
	q := `SELECT` + invitationColumns + `
		FROM competition_invitations
		WHERE to_team_id = $1
		ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("competition.ListInvitationsForTeam: %w", err)
	}
	defer rows.Close()

	var result []*competition.Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, fmt.Errorf("competition.ListInvitationsForTeam: scan: %w", err)
		}
		result = append(result, inv)
	}
	return result, rows.Err()
}

// ExpireStaleInvitations vence de una sola pasada todas las invitaciones sin
// responder cuyo plazo pasó. La entrada del equipo se deja como está: quedó en
// 'invited' y esa es la verdad, nunca dijo que sí ni que no.
func (r *CompetitionRepository) ExpireStaleInvitations(ctx context.Context, now time.Time) error {
	const q = `
		UPDATE competition_invitations
		SET status = 'expired'
		WHERE status = 'sent' AND expires_at < $1`
	if _, err := r.pool.Exec(ctx, q, now); err != nil {
		return fmt.Errorf("competition.ExpireStaleInvitations: %w", err)
	}
	return nil
}

// CreateInvitation inserta la invitación y deja la entrada del equipo en
// 'invited', en una transacción. El índice parcial de la tabla impide que haya
// dos invitaciones abiertas para el mismo equipo y competencia.
func (r *CompetitionRepository) CreateInvitation(ctx context.Context, inv *competition.Invitation) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("competition.CreateInvitation: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const insertInv = `
		INSERT INTO competition_invitations
			(competition_id, from_team_id, to_team_id, status, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err = tx.QueryRow(ctx, insertInv,
		inv.CompetitionID, inv.FromTeamID, inv.ToTeamID, inv.Status, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("competition.CreateInvitation: insert: %w", err)
	}

	const upsertEntry = `
		INSERT INTO competition_entries (competition_id, team_id, status)
		VALUES ($1, $2, 'invited')
		ON CONFLICT (competition_id, team_id) DO NOTHING`
	if _, err := tx.Exec(ctx, upsertEntry, inv.CompetitionID, inv.ToTeamID); err != nil {
		return fmt.Errorf("competition.CreateInvitation: entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("competition.CreateInvitation: commit: %w", err)
	}
	return nil
}

// RespondToInvitation resuelve la invitación y sincroniza la entrada del equipo
// en la misma transacción. Son dos escrituras que describen un solo hecho: si
// una queda sin la otra, el equipo figura aceptado sin estar inscrito, o al
// revés.
func (r *CompetitionRepository) RespondToInvitation(
	ctx context.Context, id string, accept bool, at time.Time,
) (*competition.Invitation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("competition.RespondToInvitation: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// El WHERE status = 'sent' es el que hace idempotente la operación: si otra
	// petición ya respondió, ésta no afecta filas y se corta.
	invStatus := competition.InvitationDeclined
	entryStatus := competition.EntryDeclined
	var joinedAt *time.Time
	if accept {
		invStatus = competition.InvitationAccepted
		entryStatus = competition.EntryActive
		joinedAt = &at
	}

	q := `UPDATE competition_invitations
		SET status = $1, responded_at = $2
		WHERE id = $3 AND status = 'sent'
		RETURNING` + invitationColumns
	inv, err := scanInvitation(tx.QueryRow(ctx, q, invStatus, at, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, competition.ErrInvitationClosed
	}
	if err != nil {
		return nil, fmt.Errorf("competition.RespondToInvitation: update: %w", err)
	}

	const entryQ = `
		INSERT INTO competition_entries (competition_id, team_id, status, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (competition_id, team_id) DO UPDATE
			SET status = EXCLUDED.status,
			    joined_at = COALESCE(competition_entries.joined_at, EXCLUDED.joined_at)`
	if _, err := tx.Exec(ctx, entryQ, inv.CompetitionID, inv.ToTeamID, entryStatus, joinedAt); err != nil {
		return nil, fmt.Errorf("competition.RespondToInvitation: entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("competition.RespondToInvitation: commit: %w", err)
	}
	return inv, nil
}

// nullIfEmpty evita guardar cadenas vacías donde la columna admite NULL:
// "sin lugar" y "lugar en blanco" son lo mismo y conviene que la base lo diga.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
