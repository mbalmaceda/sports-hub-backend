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
// El rol viene de memberships.role, no de users (el rol es por equipo).
const teamMemberQuery = `
	SELECT
		m.id, m.user_id, m.team_id,
		u.name, COALESCE(u.avatar_url,''), u.email, COALESCE(u.phone,''),
		m.role, m.jersey_number, COALESCE(m.position,''), m.status, m.joined_at
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
	const q = `SELECT id, user_id, team_id, role, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE id = $1`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.FindByID: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) FindByUserAndTeam(ctx context.Context, userID, teamID string) (*membership.Membership, error) {
	const q = `SELECT id, user_id, team_id, role, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE user_id = $1 AND team_id = $2`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, userID, teamID).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
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

// ListByUser lista todos los equipos (y el rol en cada uno) del usuario autenticado.
// El mobile la usa para resolver la sesión activa tras login/refresh.
func (r *RosterRepository) ListByUser(ctx context.Context, userID string) ([]*membership.TeamMember, error) {
	q := teamMemberQuery + ` WHERE m.user_id = $1 ORDER BY m.joined_at`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("membership.ListByUser: %w", err)
	}
	defer rows.Close()

	var members []*membership.TeamMember
	for rows.Next() {
		m, err := scanTeamMember(rows)
		if err != nil {
			return nil, fmt.Errorf("membership.ListByUser scan: %w", err)
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
		INSERT INTO memberships (user_id, team_id, role, status, jersey_number, position)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))
		RETURNING id, joined_at`
	err := r.pool.QueryRow(ctx, q, m.UserID, m.TeamID, m.Role, m.Status, m.JerseyNumber, m.Position).
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

// UpdateRole cambia el rol de un miembro dentro del equipo (ej: promover a treasurer).
func (r *RosterRepository) UpdateRole(ctx context.Context, id string, role membership.Role) error {
	_, err := r.pool.Exec(ctx, `UPDATE memberships SET role = $1 WHERE id = $2`, role, id)
	if err != nil {
		return fmt.Errorf("membership.UpdateRole: %w", err)
	}
	return nil
}
