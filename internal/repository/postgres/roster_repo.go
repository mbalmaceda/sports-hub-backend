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
		m.role, m.kind, m.plays_as_player, m.jersey_number, COALESCE(m.position,''), m.status, m.joined_at
	FROM memberships m
	JOIN users u ON u.id = m.user_id`

func scanTeamMember(row pgx.Row) (*membership.TeamMember, error) {
	m := &membership.TeamMember{}
	err := row.Scan(
		&m.MembershipID, &m.UserID, &m.TeamID,
		&m.FullName, &m.AvatarURL, &m.Email, &m.Phone,
		&m.Role, &m.Kind, &m.PlaysAsPlayer, &m.JerseyNumber, &m.Position, &m.Status, &m.JoinedAt,
	)
	return m, err
}

// rosterListQuery es el plantel como lo ve el resto del equipo, y a propósito
// no trae email ni teléfono.
//
// El listado responde "quiénes juegan acá"; los datos de contacto son otra
// pregunta y se responden de a uno, en la ficha del jugador (`GetMemberByID`).
// Mandarlos en la lista entregaba la agenda completa del club en un solo GET, y
// no hay ninguna pantalla que los use ahí: el único lugar que los muestra es
// `app/player/[membershipId]`, que pide la ficha.
//
// Se resuelve en el SELECT y no borrando campos después de leerlos: el dato que
// no sale de Postgres no se puede filtrar por descuido más arriba.
const rosterListQuery = `
	SELECT
		m.id, m.user_id, m.team_id,
		u.name, COALESCE(u.avatar_url,''),
		m.role, m.kind, m.plays_as_player, m.jersey_number, COALESCE(m.position,''), m.status, m.joined_at
	FROM memberships m
	JOIN users u ON u.id = m.user_id`

func scanRosterMember(row pgx.Row) (*membership.TeamMember, error) {
	m := &membership.TeamMember{}
	err := row.Scan(
		&m.MembershipID, &m.UserID, &m.TeamID,
		&m.FullName, &m.AvatarURL,
		&m.Role, &m.Kind, &m.PlaysAsPlayer, &m.JerseyNumber, &m.Position, &m.Status, &m.JoinedAt,
	)
	return m, err
}

func (r *RosterRepository) FindByID(ctx context.Context, id string) (*membership.Membership, error) {
	const q = `SELECT id, user_id, team_id, role, kind, plays_as_player, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE id = $1`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role, &m.Kind, &m.PlaysAsPlayer, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.FindByID: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) FindByUserAndTeam(ctx context.Context, userID, teamID string) (*membership.Membership, error) {
	const q = `SELECT id, user_id, team_id, role, kind, plays_as_player, status, jersey_number, COALESCE(position,''), joined_at FROM memberships WHERE user_id = $1 AND team_id = $2`
	m := &membership.Membership{}
	err := r.pool.QueryRow(ctx, q, userID, teamID).
		Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role, &m.Kind, &m.PlaysAsPlayer, &m.Status, &m.JerseyNumber, &m.Position, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, membership.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("membership.FindByUserAndTeam: %w", err)
	}
	return m, nil
}

func (r *RosterRepository) ListByTeam(ctx context.Context, teamID string) ([]*membership.TeamMember, error) {
	q := rosterListQuery + ` WHERE m.team_id = $1 ORDER BY u.name`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("membership.ListByTeam: %w", err)
	}
	defer rows.Close()

	members, err := collect(rows, scanRosterMember)
	if err != nil {
		return nil, fmt.Errorf("membership.ListByTeam scan: %w", err)
	}
	return members, nil
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

	members, err := collect(rows, scanTeamMember)
	if err != nil {
		return nil, fmt.Errorf("membership.ListByUser scan: %w", err)
	}
	return members, nil
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
	// Kind vacío es 'member': el alta normal no lo manda y no tiene por qué
	// enterarse de que los invitados existen.
	kind := m.Kind
	if kind == "" {
		kind = membership.KindMember
	}

	const q = `
		INSERT INTO memberships (user_id, team_id, role, kind, plays_as_player, status, jersey_number, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,''))
		RETURNING id, joined_at`
	err := r.pool.QueryRow(ctx, q, m.UserID, m.TeamID, m.Role, kind, m.PlaysAsPlayer, m.Status, m.JerseyNumber, m.Position).
		Scan(&m.ID, &m.JoinedAt)
	if err != nil {
		return fmt.Errorf("membership.Create: %w", err)
	}
	m.Kind = kind
	return nil
}

func (r *RosterRepository) UpdateStatus(ctx context.Context, id string, status membership.Status) error {
	_, err := r.pool.Exec(ctx, `UPDATE memberships SET status = $1 WHERE id = $2`, status, id)
	return err
}

// PromoteGuest pasa a un invitado al plantel. Ver el contrato en la interfaz.
//
// El WHERE acota a los invitados: correrlo sobre un miembro normal no tendría
// nada que cambiar, pero dejar la condición afuera invita a que alguien use esto
// como un "resetear membresía" que no es.
func (r *RosterRepository) PromoteGuest(ctx context.Context, id string) error {
	const q = `UPDATE memberships SET kind = 'member' WHERE id = $1 AND kind = 'guest'`
	if _, err := r.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("membership.PromoteGuest: %w", err)
	}
	return nil
}

// UpdateRole cambia el rol de un miembro dentro del equipo (ej: promover a treasurer).
func (r *RosterRepository) UpdateRole(ctx context.Context, id string, role membership.Role) error {
	_, err := r.pool.Exec(ctx, `UPDATE memberships SET role = $1 WHERE id = $2`, role, id)
	if err != nil {
		return fmt.Errorf("membership.UpdateRole: %w", err)
	}
	return nil
}
