package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
)

type MatchRepository struct {
	pool *pgxpool.Pool
}

func NewMatchRepository(pool *pgxpool.Pool) *MatchRepository {
	return &MatchRepository{pool: pool}
}

const matchColumns = `
	id, competition_id, home_team_id, away_team_id, scheduled_at, venue, status, created_at`

func scanMatch(row pgx.Row) (*match.Match, error) {
	m := &match.Match{}
	var venue *string
	err := row.Scan(
		&m.ID, &m.CompetitionID, &m.HomeTeamID, &m.AwayTeamID,
		&m.ScheduledAt, &venue, &m.Status, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if venue != nil {
		m.Venue = *venue
	}
	return m, nil
}

func collectMatches(rows pgx.Rows) ([]*match.Match, error) {
	return collect(rows, scanMatch)
}

func (r *MatchRepository) FindByID(ctx context.Context, id string) (*match.Match, error) {
	q := `SELECT` + matchColumns + ` FROM matches WHERE id = $1`
	m, err := scanMatch(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, match.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("match.FindByID: %w", err)
	}
	return m, nil
}

func (r *MatchRepository) ListByCompetition(ctx context.Context, competitionID string) ([]*match.Match, error) {
	q := `SELECT` + matchColumns + ` FROM matches WHERE competition_id = $1 ORDER BY scheduled_at`
	rows, err := r.pool.Query(ctx, q, competitionID)
	if err != nil {
		return nil, fmt.Errorf("match.ListByCompetition: %w", err)
	}
	defer rows.Close()
	return collectMatches(rows)
}

func (r *MatchRepository) ListByTeam(ctx context.Context, teamID string) ([]*match.Match, error) {
	q := `SELECT` + matchColumns + `
		FROM matches
		WHERE home_team_id = $1 OR away_team_id = $1
		ORDER BY scheduled_at`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("match.ListByTeam: %w", err)
	}
	defer rows.Close()
	return collectMatches(rows)
}

// ListByTeamOnDate busca partidos del equipo el mismo día, para avisar choques
// al agendar. Compara por fecha y no por instante: dos partidos el mismo día
// son un problema aunque estén separados por horas.
func (r *MatchRepository) ListByTeamOnDate(ctx context.Context, teamID string, day time.Time) ([]*match.Match, error) {
	q := `SELECT` + matchColumns + `
		FROM matches
		WHERE (home_team_id = $1 OR away_team_id = $1)
		  AND status <> 'cancelled'
		  AND scheduled_at::date = $2::date
		ORDER BY scheduled_at`
	rows, err := r.pool.Query(ctx, q, teamID, day)
	if err != nil {
		return nil, fmt.Errorf("match.ListByTeamOnDate: %w", err)
	}
	defer rows.Close()
	return collectMatches(rows)
}

func (r *MatchRepository) Create(ctx context.Context, m *match.Match) error {
	const q = `
		INSERT INTO matches (competition_id, home_team_id, away_team_id, scheduled_at, venue, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q,
		m.CompetitionID, m.HomeTeamID, m.AwayTeamID, m.ScheduledAt, nullIfEmpty(m.Venue), m.Status,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return fmt.Errorf("match.Create: %w", err)
	}
	return nil
}

func (r *MatchRepository) UpdateStatus(ctx context.Context, id string, status match.Status) error {
	const q = `UPDATE matches SET status = $1 WHERE id = $2`
	if _, err := r.pool.Exec(ctx, q, status, id); err != nil {
		return fmt.Errorf("match.UpdateStatus: %w", err)
	}
	return nil
}

// ─── Convocatorias ───────────────────────────────────────────────────────────

// Ojo con el espacio inicial: estas constantes se concatenan a `SELECT`/`RETURNING`
// crudos (`SELECT` + callupColumns), y sin el espacio saldría `SELECTid`.
const callupColumns = ` id, match_id, membership_id, status, called_at, responded_at`

func scanCallup(row pgx.Row) (*match.Callup, error) {
	cu := &match.Callup{}
	err := row.Scan(&cu.ID, &cu.MatchID, &cu.MembershipID, &cu.Status, &cu.CalledAt, &cu.RespondedAt)
	return cu, err
}

func collectCallups(rows pgx.Rows) ([]*match.Callup, error) {
	return collect(rows, scanCallup)
}

func (r *MatchRepository) ListCallups(ctx context.Context, matchID string) ([]*match.Callup, error) {
	q := `SELECT` + callupColumns + ` FROM match_callups WHERE match_id = $1 ORDER BY called_at`
	rows, err := r.pool.Query(ctx, q, matchID)
	if err != nil {
		return nil, fmt.Errorf("match.ListCallups: %w", err)
	}
	defer rows.Close()
	return collectCallups(rows)
}

func (r *MatchRepository) ListCallupsByMembership(ctx context.Context, membershipID string) ([]*match.Callup, error) {
	q := `SELECT` + callupColumns + ` FROM match_callups WHERE membership_id = $1 ORDER BY called_at DESC`
	rows, err := r.pool.Query(ctx, q, membershipID)
	if err != nil {
		return nil, fmt.Errorf("match.ListCallupsByMembership: %w", err)
	}
	defer rows.Close()
	return collectCallups(rows)
}

// CallUp convoca a varios jugadores de una vez.
//
// El ON CONFLICT DO NOTHING es la clave: a quien ya fue convocado no se le toca
// el registro, así que tocar "Convocar a todos" no borra los "voy" que ya
// llegaron. Devuelve el estado final de todos los pedidos, convocados ahora o
// antes, que es lo que la pantalla necesita para repintar.
func (r *MatchRepository) CallUp(ctx context.Context, matchID string, membershipIDs []string) ([]*match.Callup, error) {
	if len(membershipIDs) == 0 {
		return []*match.Callup{}, nil
	}

	const insert = `
		INSERT INTO match_callups (match_id, membership_id, status)
		SELECT $1, unnest($2::uuid[]), 'called'
		ON CONFLICT (match_id, membership_id) DO NOTHING`
	if _, err := r.pool.Exec(ctx, insert, matchID, membershipIDs); err != nil {
		return nil, fmt.Errorf("match.CallUp: insert: %w", err)
	}

	q := `SELECT` + callupColumns + `
		FROM match_callups
		WHERE match_id = $1 AND membership_id = ANY($2::uuid[])
		ORDER BY called_at`
	rows, err := r.pool.Query(ctx, q, matchID, membershipIDs)
	if err != nil {
		return nil, fmt.Errorf("match.CallUp: select: %w", err)
	}
	defer rows.Close()
	return collectCallups(rows)
}

// Respond registra el "voy" o el "no puedo".
//
// Hace upsert en vez de update porque un jugador puede llegar desde una
// notificación cuya convocatoria ya no está —por ejemplo si se rehizo la
// nómina—, y es preferible registrar su respuesta a devolverle un error.
func (r *MatchRepository) Respond(
	ctx context.Context, matchID, membershipID string, attending bool, at time.Time,
) (*match.Callup, error) {
	status := match.CallupDeclined
	if attending {
		status = match.CallupConfirmed
	}

	q := `
		INSERT INTO match_callups (match_id, membership_id, status, responded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (match_id, membership_id) DO UPDATE
			SET status = EXCLUDED.status, responded_at = EXCLUDED.responded_at
		RETURNING` + callupColumns
	cu, err := scanCallup(r.pool.QueryRow(ctx, q, matchID, membershipID, status, at))
	if err != nil {
		return nil, fmt.Errorf("match.Respond: %w", err)
	}
	return cu, nil
}
