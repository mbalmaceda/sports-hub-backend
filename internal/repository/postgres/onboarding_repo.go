package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/onboarding"
)

type OnboardingRepository struct {
	pool *pgxpool.Pool
}

func NewOnboardingRepository(pool *pgxpool.Pool) *OnboardingRepository {
	return &OnboardingRepository{pool: pool}
}

// normalizeTaxID saca puntos, guiones y espacios. Nadie tipea un RUT igual dos
// veces, y exigir el formato exacto haría que la búsqueda falle por un punto.
func normalizeTaxID(value string) string {
	replacer := strings.NewReplacer(".", "", "-", "", " ", "")
	return strings.ToLower(replacer.Replace(value))
}

func (r *OnboardingRepository) FindPerson(
	ctx context.Context, method onboarding.LookupMethod, value string,
) (*onboarding.Person, error) {
	needle := strings.TrimSpace(value)
	if needle == "" {
		return nil, onboarding.ErrPersonNotFound
	}

	// La comparación se normaliza del lado de la base para que el índice siga
	// siendo útil en el caso del correo, y para que el RUT tolere el formato.
	var q string
	var arg string
	switch method {
	case onboarding.LookupByTaxID:
		q = `
			SELECT u.id, u.full_name, u.email, COALESCE(u.tax_id, ''),
			       EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.status = 'active')
			FROM users u
			WHERE REPLACE(REPLACE(REPLACE(LOWER(u.tax_id), '.', ''), '-', ''), ' ', '') = $1`
		arg = normalizeTaxID(needle)
	default:
		q = `
			SELECT u.id, u.full_name, u.email, COALESCE(u.tax_id, ''),
			       EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.status = 'active')
			FROM users u
			WHERE LOWER(u.email) = $1`
		arg = strings.ToLower(needle)
	}

	p := &onboarding.Person{}
	err := r.pool.QueryRow(ctx, q, arg).Scan(&p.UserID, &p.FullName, &p.Email, &p.TaxID, &p.HasTeam)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, onboarding.ErrPersonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("onboarding.FindPerson: %w", err)
	}
	return p, nil
}

// ─── Invitaciones del equipo hacia una persona ───────────────────────────────

const invitationCols = `
	id, team_id, invited_by_user_id, user_id, status, created_at, responded_at`

func scanTeamInvitation(row pgx.Row) (*onboarding.TeamInvitation, error) {
	inv := &onboarding.TeamInvitation{}
	err := row.Scan(
		&inv.ID, &inv.TeamID, &inv.InvitedByUserID, &inv.UserID,
		&inv.Status, &inv.CreatedAt, &inv.RespondedAt,
	)
	return inv, err
}

func collectInvitations(rows pgx.Rows) ([]*onboarding.TeamInvitation, error) {
	var result []*onboarding.TeamInvitation
	for rows.Next() {
		inv, err := scanTeamInvitation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, inv)
	}
	return result, rows.Err()
}

func (r *OnboardingRepository) ListInvitationsForTeam(ctx context.Context, teamID string) ([]*onboarding.TeamInvitation, error) {
	q := `SELECT` + invitationCols + ` FROM team_invitations WHERE team_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("onboarding.ListInvitationsForTeam: %w", err)
	}
	defer rows.Close()
	return collectInvitations(rows)
}

func (r *OnboardingRepository) ListInvitationsForUser(ctx context.Context, userID string) ([]*onboarding.TeamInvitation, error) {
	q := `SELECT` + invitationCols + ` FROM team_invitations WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("onboarding.ListInvitationsForUser: %w", err)
	}
	defer rows.Close()
	return collectInvitations(rows)
}

func (r *OnboardingRepository) FindInvitation(ctx context.Context, id string) (*onboarding.TeamInvitation, error) {
	q := `SELECT` + invitationCols + ` FROM team_invitations WHERE id = $1`
	inv, err := scanTeamInvitation(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, onboarding.ErrInvitationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("onboarding.FindInvitation: %w", err)
	}
	return inv, nil
}

func (r *OnboardingRepository) CreateInvitation(ctx context.Context, inv *onboarding.TeamInvitation) error {
	const q = `
		INSERT INTO team_invitations (team_id, invited_by_user_id, user_id, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q, inv.TeamID, inv.InvitedByUserID, inv.UserID, inv.Status).
		Scan(&inv.ID, &inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("onboarding.CreateInvitation: %w", err)
	}
	return nil
}

func (r *OnboardingRepository) RespondToInvitation(
	ctx context.Context, id string, accept bool, at time.Time,
) (*onboarding.TeamInvitation, error) {
	return r.resolve(ctx, resolveInput{
		id:     id,
		accept: accept,
		at:     at,
		update: `UPDATE team_invitations SET status = $1, responded_at = $2
		         WHERE id = $3 AND status = 'sent'
		         RETURNING` + invitationCols,
		acceptedStatus: string(onboarding.InvitationAccepted),
		declinedStatus: string(onboarding.InvitationDeclined),
	})
}

// ─── Solicitudes de la persona hacia el equipo ───────────────────────────────

// Las solicitudes se leen con el nombre y el correo embebidos: la pantalla del
// manager los muestra en cada fila, y resolverlos aparte sería una consulta por
// solicitud para pintar un nombre.
const joinRequestCols = `
	r.id, r.team_id, r.user_id, COALESCE(r.message, ''), r.status,
	r.created_at, r.responded_at, r.resolved_by,
	u.full_name, u.email, COALESCE(u.tax_id, '')`

func scanJoinRequest(row pgx.Row) (*onboarding.JoinRequest, error) {
	req := &onboarding.JoinRequest{}
	err := row.Scan(
		&req.ID, &req.TeamID, &req.UserID, &req.Message, &req.Status,
		&req.CreatedAt, &req.RespondedAt, &req.ResolvedBy,
		&req.FullName, &req.Email, &req.TaxID,
	)
	return req, err
}

func (r *OnboardingRepository) listJoinRequests(ctx context.Context, where string, arg string) ([]*onboarding.JoinRequest, error) {
	q := `SELECT` + joinRequestCols + `
		FROM join_requests r JOIN users u ON u.id = r.user_id
		WHERE ` + where + `
		ORDER BY r.created_at DESC`
	rows, err := r.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("onboarding.listJoinRequests: %w", err)
	}
	defer rows.Close()

	var result []*onboarding.JoinRequest
	for rows.Next() {
		req, err := scanJoinRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("onboarding.listJoinRequests: scan: %w", err)
		}
		result = append(result, req)
	}
	return result, rows.Err()
}

func (r *OnboardingRepository) ListJoinRequestsForTeam(ctx context.Context, teamID string) ([]*onboarding.JoinRequest, error) {
	return r.listJoinRequests(ctx, "r.team_id = $1", teamID)
}

func (r *OnboardingRepository) ListJoinRequestsForUser(ctx context.Context, userID string) ([]*onboarding.JoinRequest, error) {
	return r.listJoinRequests(ctx, "r.user_id = $1", userID)
}

func (r *OnboardingRepository) FindJoinRequest(ctx context.Context, id string) (*onboarding.JoinRequest, error) {
	q := `SELECT` + joinRequestCols + `
		FROM join_requests r JOIN users u ON u.id = r.user_id
		WHERE r.id = $1`
	req, err := scanJoinRequest(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, onboarding.ErrJoinRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("onboarding.FindJoinRequest: %w", err)
	}
	return req, nil
}

func (r *OnboardingRepository) CreateJoinRequest(ctx context.Context, req *onboarding.JoinRequest) error {
	const q = `
		INSERT INTO join_requests (team_id, user_id, message, status)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q, req.TeamID, req.UserID, req.Message, req.Status).
		Scan(&req.ID, &req.CreatedAt)
	if err != nil {
		return fmt.Errorf("onboarding.CreateJoinRequest: %w", err)
	}
	return nil
}

func (r *OnboardingRepository) RespondToJoinRequest(
	ctx context.Context, id string, accept bool, resolvedBy string, at time.Time,
) (*onboarding.JoinRequest, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("onboarding.RespondToJoinRequest: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	status := onboarding.JoinDeclined
	if accept {
		status = onboarding.JoinAccepted
	}

	q := `UPDATE join_requests
		SET status = $1, responded_at = $2, resolved_by = $3
		WHERE id = $4 AND status = 'pending'
		RETURNING id, team_id, user_id, COALESCE(message, ''), status, created_at, responded_at, resolved_by`
	req := &onboarding.JoinRequest{}
	err = tx.QueryRow(ctx, q, status, at, resolvedBy, id).Scan(
		&req.ID, &req.TeamID, &req.UserID, &req.Message, &req.Status,
		&req.CreatedAt, &req.RespondedAt, &req.ResolvedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, onboarding.ErrAlreadyAnswered
	}
	if err != nil {
		return nil, fmt.Errorf("onboarding.RespondToJoinRequest: update: %w", err)
	}

	// El alta va en la misma transacción que la aceptación. Partirlo dejaría
	// solicitudes aceptadas sin jugador en el plantel, que es la clase de
	// inconsistencia que después nadie sabe cómo reparar.
	if accept {
		const addMember = `
			INSERT INTO memberships (user_id, team_id, role, status)
			VALUES ($1, $2, 'player', 'active')
			ON CONFLICT (user_id, team_id) DO UPDATE SET status = 'active'`
		if _, err := tx.Exec(ctx, addMember, req.UserID, req.TeamID); err != nil {
			return nil, fmt.Errorf("onboarding.RespondToJoinRequest: membership: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboarding.RespondToJoinRequest: commit: %w", err)
	}
	return req, nil
}

// resolveInput agrupa lo que comparten responder una invitación y una
// solicitud: cambiar el estado si sigue abierta, y dar de alta si se acepta.
type resolveInput struct {
	id             string
	accept         bool
	at             time.Time
	update         string
	acceptedStatus string
	declinedStatus string
}

func (r *OnboardingRepository) resolve(ctx context.Context, in resolveInput) (*onboarding.TeamInvitation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("onboarding.resolve: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	status := in.declinedStatus
	if in.accept {
		status = in.acceptedStatus
	}

	inv, err := scanTeamInvitation(tx.QueryRow(ctx, in.update, status, in.at, in.id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, onboarding.ErrAlreadyAnswered
	}
	if err != nil {
		return nil, fmt.Errorf("onboarding.resolve: update: %w", err)
	}

	if in.accept {
		const addMember = `
			INSERT INTO memberships (user_id, team_id, role, status)
			VALUES ($1, $2, 'player', 'active')
			ON CONFLICT (user_id, team_id) DO UPDATE SET status = 'active'`
		if _, err := tx.Exec(ctx, addMember, inv.UserID, inv.TeamID); err != nil {
			return nil, fmt.Errorf("onboarding.resolve: membership: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboarding.resolve: commit: %w", err)
	}
	return inv, nil
}
