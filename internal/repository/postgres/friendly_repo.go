package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
)

type FriendlyRepository struct {
	pool *pgxpool.Pool
}

func NewFriendlyRepository(pool *pgxpool.Pool) *FriendlyRepository {
	return &FriendlyRepository{pool: pool}
}

const challengeColumns = `
	id, competition_id, challenger_team_id, challenged_team_id, status, expires_at, created_at,
	(SELECT fp.proposed_by_team_id FROM friendly_proposals fp
		WHERE fp.challenge_id = friendly_challenges.id
		ORDER BY fp.created_at DESC
		LIMIT 1) AS last_proposed_by_team_id`

func scanChallenge(row pgx.Row) (*friendly.Challenge, error) {
	ch := &friendly.Challenge{}
	err := row.Scan(
		&ch.ID, &ch.CompetitionID, &ch.ChallengerTeamID, &ch.ChallengedTeamID,
		&ch.Status, &ch.ExpiresAt, &ch.CreatedAt, &ch.LastProposedByTeamID,
	)
	return ch, err
}

func (r *FriendlyRepository) FindByID(ctx context.Context, id string) (*friendly.Challenge, error) {
	q := `SELECT` + challengeColumns + ` FROM friendly_challenges WHERE id = $1`
	ch, err := scanChallenge(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, friendly.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("friendly.FindByID: %w", err)
	}
	return ch, nil
}

func (r *FriendlyRepository) ListByTeam(ctx context.Context, teamID string) ([]*friendly.Challenge, error) {
	q := `SELECT` + challengeColumns + `
		FROM friendly_challenges
		WHERE challenger_team_id = $1 OR challenged_team_id = $1
		ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("friendly.ListByTeam: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, scanChallenge)
	if err != nil {
		return nil, fmt.Errorf("friendly.ListByTeam: scan: %w", err)
	}
	return result, nil
}

// Create inserta el desafío y su primera propuesta juntos. Un desafío sin
// propuesta no tiene fecha ni lugar: no habría nada que responder.
func (r *FriendlyRepository) Create(ctx context.Context, ch *friendly.Challenge, first *friendly.Proposal) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("friendly.Create: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const insertChallenge = `
		INSERT INTO friendly_challenges
			(competition_id, challenger_team_id, challenged_team_id, status, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err = tx.QueryRow(ctx, insertChallenge,
		ch.CompetitionID, ch.ChallengerTeamID, ch.ChallengedTeamID, ch.Status, ch.ExpiresAt,
	).Scan(&ch.ID, &ch.CreatedAt)
	if err != nil {
		return fmt.Errorf("friendly.Create: challenge: %w", err)
	}

	first.ChallengeID = ch.ID
	const insertProposal = `
		INSERT INTO friendly_proposals
			(challenge_id, proposed_by_team_id, proposed_start_at, proposed_venue, message)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err = tx.QueryRow(ctx, insertProposal,
		first.ChallengeID, first.ProposedByTeamID, first.ProposedStartAt,
		nullIfEmpty(first.ProposedVenue), nullIfEmpty(first.Message),
	).Scan(&first.ID, &first.CreatedAt)
	if err != nil {
		return fmt.Errorf("friendly.Create: proposal: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("friendly.Create: commit: %w", err)
	}
	return nil
}

func (r *FriendlyRepository) UpdateStatus(ctx context.Context, id string, status friendly.Status) error {
	const q = `UPDATE friendly_challenges SET status = $1 WHERE id = $2`
	if _, err := r.pool.Exec(ctx, q, status, id); err != nil {
		return fmt.Errorf("friendly.UpdateStatus: %w", err)
	}
	return nil
}

// ExpireStale vence de una sola pasada todo lo que se pasó de plazo. Un UPDATE
// sin filas que tocar no escribe nada, así que en el caso normal —que es que no
// haya nada vencido— sale gratis.
func (r *FriendlyRepository) ExpireStale(ctx context.Context, now time.Time) ([]string, error) {
	const q = `
		UPDATE friendly_challenges
		SET status = 'expired'
		WHERE status IN ('pending', 'countered') AND expires_at < $1
		RETURNING competition_id`
	rows, err := r.pool.Query(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("friendly.ExpireStale: %w", err)
	}
	defer rows.Close()

	// RowTo[string] es el scanner que trae pgx para resultados de una sola
	// columna: no hace falta escribir uno para leer ids.
	competitionIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("friendly.ExpireStale: scan: %w", err)
	}
	return competitionIDs, nil
}

const proposalColumns = `
	id, challenge_id, proposed_by_team_id, proposed_start_at, proposed_venue, message, created_at`

func scanProposal(row pgx.Row) (*friendly.Proposal, error) {
	p := &friendly.Proposal{}
	var venue, message *string
	err := row.Scan(
		&p.ID, &p.ChallengeID, &p.ProposedByTeamID, &p.ProposedStartAt,
		&venue, &message, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if venue != nil {
		p.ProposedVenue = *venue
	}
	if message != nil {
		p.Message = *message
	}
	return p, nil
}

func (r *FriendlyRepository) ListProposals(ctx context.Context, challengeID string) ([]*friendly.Proposal, error) {
	q := `SELECT` + proposalColumns + `
		FROM friendly_proposals
		WHERE challenge_id = $1
		ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, challengeID)
	if err != nil {
		return nil, fmt.Errorf("friendly.ListProposals: %w", err)
	}
	defer rows.Close()

	result, err := collect(rows, scanProposal)
	if err != nil {
		return nil, fmt.Errorf("friendly.ListProposals: scan: %w", err)
	}
	return result, nil
}

func (r *FriendlyRepository) LatestProposal(ctx context.Context, challengeID string) (*friendly.Proposal, error) {
	q := `SELECT` + proposalColumns + `
		FROM friendly_proposals
		WHERE challenge_id = $1
		ORDER BY created_at DESC
		LIMIT 1`
	p, err := scanProposal(r.pool.QueryRow(ctx, q, challengeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, friendly.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("friendly.LatestProposal: %w", err)
	}
	return p, nil
}

// AddProposal guarda la contraoferta y mueve el desafío a 'countered' en la
// misma transacción: la propuesta nueva ES el cambio de estado.
func (r *FriendlyRepository) AddProposal(ctx context.Context, p *friendly.Proposal) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("friendly.AddProposal: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const insert = `
		INSERT INTO friendly_proposals
			(challenge_id, proposed_by_team_id, proposed_start_at, proposed_venue, message)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err = tx.QueryRow(ctx, insert,
		p.ChallengeID, p.ProposedByTeamID, p.ProposedStartAt,
		nullIfEmpty(p.ProposedVenue), nullIfEmpty(p.Message),
	).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return fmt.Errorf("friendly.AddProposal: insert: %w", err)
	}

	const updateStatus = `
		UPDATE friendly_challenges SET status = 'countered'
		WHERE id = $1 AND status IN ('pending', 'countered')`
	if _, err := tx.Exec(ctx, updateStatus, p.ChallengeID); err != nil {
		return fmt.Errorf("friendly.AddProposal: status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("friendly.AddProposal: commit: %w", err)
	}
	return nil
}
