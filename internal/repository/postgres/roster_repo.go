package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
)

type RosterRepository struct {
	pool *pgxpool.Pool
}

func NewRosterRepository(pool *pgxpool.Pool) *RosterRepository {
	return &RosterRepository{pool: pool}
}

// teamMemberQuery hace JOIN entre memberships y users para construir TeamMember.
const teamMemberQuery = `
	SELECT
		m.id, m.user_id, m.team_id,
		u.name, COALESCE(u.avatar_url,''), u.email, COALESCE(u.phone,''),
		u.role, m.jersey_number, COALESCE(m.position,''), m.status, m.joined_at
	FROM memberships m
	JOIN users u ON u.id = m.user_id`

func scanTeamMember(row pgx.Row) (*membership.TeamMember, error) {
	m := &membership.TeamMember{}
	err := row.Scan(
		&m.MembershipID, &m.UserID, &m.TeamID,
		&m.FullName, &m.AvatarURL, &m.Email, &m.Phone,
		&m.Role, &m.JerseyNumber, &m.Position, &m.Status, &m.JoinedAt,
	)
	return m, err
}

func (r *RosterRepository) FindByID(ctx context.Context, id string) (*membership.Membership, error) {
	const q = `SELECT id, user_id, team_id, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE id = $1`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.FindByID: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) FindByUserAndTeam(ctx context.Context, userID, teamID string) (*membership.Membership, error) {
	const q = `SELECT id, user_id, team_id, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE user_id = $1 AND team_id = $2`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, userID, teamID).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.FindByUserAndTeam: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) ListByTeam(ctx context.Context, teamID string) ([]*membership.TeamMember, error) {
	q := teamMemberQuery + ` WHERE m.team_id = $1 ORDER BY u.name`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("membership.ListByTeam: %w", err)
	}
	defer rows.Close()

	var members []*membership.TeamMember
	for rows.Next() {
		m, err := scanTeamMember(rows)
		if err != nil {
			return nil, fmt.Errorf("membership.ListByTeam scan: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (r *RosterRepository) GetMemberByID(ctx context.Context, membershipID string) (*membership.TeamMember, error) {
	q := teamMemberQuery + ` WHERE m.id = $1`
	m, err := scanTeamMember(r.pool.QueryRow(ctx, q, membershipID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.GetMemberByID: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) Create(ctx context.Context, m *membership.Membership) error {
	const q = `
		INSERT INTO memberships (user_id, team_id, status, jersey_number, position)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		RETURNING id, joined_at`
	err := r.pool.QueryRow(ctx, q, m.UserID, m.TeamID, m.Status, m.JerseyNumber, m.Position).
		Scan(&m.ID, &m.JoinedAt)
	if err != nil {
		return fmt.Errorf("membership.Create: %w", err)
	}
	return nil
}

func (r *RosterRepository) UpdateStatus(ctx context.Context, id string, status membership.Status) error {
	_, err := r.pool.Exec(ctx, `UPDATE memberships SET status = $1 WHERE id = $2`, status, id)
	return err
}
