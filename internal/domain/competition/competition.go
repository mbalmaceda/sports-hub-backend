package competition

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound           = errors.New("competition not found")
	ErrEntryNotFound      = errors.New("competition entry not found")
	ErrInvitationNotFound = errors.New("competition invitation not found")
	// ErrInvitationClosed protege de responder dos veces la misma invitación,
	// por ejemplo desde una notificación vieja que quedó en el teléfono.
	ErrInvitationClosed = errors.New("competition invitation is no longer open")
)

type Type string

const (
	TypeFriendly   Type = "friendly"
	TypeTournament Type = "tournament"
	TypeLeague     Type = "league"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusActive    Status = "active"
	StatusFinished  Status = "finished"
	StatusCancelled Status = "cancelled"
)

type EntryStatus string

const (
	EntryInvited   EntryStatus = "invited"
	EntryPending   EntryStatus = "pending"
	EntryActive    EntryStatus = "active"
	EntryDeclined  EntryStatus = "declined"
	EntryWithdrawn EntryStatus = "withdrawn"
)

type InvitationStatus string

const (
	InvitationSent     InvitationStatus = "sent"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
	InvitationExpired  InvitationStatus = "expired"
	InvitationRevoked  InvitationStatus = "revoked"
)

// VenueCost es el costo del lugar, en unidades menores de la moneda.
// CLP no tiene decimales, así que 28000 es $28.000 y no $280.
type VenueCost struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type Competition struct {
	ID              string     `json:"id"`
	SportID         string     `json:"sport_id"`
	Type            Type       `json:"type"`
	Name            string     `json:"name"`
	OrganizerTeamID string     `json:"organizer_team_id"`
	Status          Status     `json:"status"`
	StartAt         *time.Time `json:"start_at,omitempty"`
	EndAt           *time.Time `json:"end_at,omitempty"`
	Venue           string     `json:"venue,omitempty"`
	PlayersPerSide  *int       `json:"players_per_side,omitempty"`
	VenueCost       *VenueCost `json:"venue_cost,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Entry struct {
	ID            string      `json:"id"`
	CompetitionID string      `json:"competition_id"`
	TeamID        string      `json:"team_id"`
	Status        EntryStatus `json:"status"`
	JoinedAt      *time.Time  `json:"joined_at,omitempty"`
}

type Invitation struct {
	ID            string           `json:"id"`
	CompetitionID string           `json:"competition_id"`
	FromTeamID    string           `json:"from_team_id"`
	ToTeamID      string           `json:"to_team_id"`
	Status        InvitationStatus `json:"status"`
	ExpiresAt     time.Time        `json:"expires_at"`
	CreatedAt     time.Time        `json:"created_at"`
	RespondedAt   *time.Time       `json:"responded_at,omitempty"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Competition, error)
	// ListByTeam devuelve las competencias que el equipo organiza o en las que
	// tiene una entrada, sin importar el estado de esa entrada.
	ListByTeam(ctx context.Context, teamID string) ([]*Competition, error)
	Create(ctx context.Context, c *Competition) error
	UpdateStatus(ctx context.Context, id string, status Status) error

	ListEntries(ctx context.Context, competitionID string) ([]*Entry, error)
	// UpsertEntry crea la entrada o actualiza su estado si el equipo ya estaba.
	UpsertEntry(ctx context.Context, e *Entry) error

	FindInvitation(ctx context.Context, id string) (*Invitation, error)
	ListInvitationsForTeam(ctx context.Context, teamID string) ([]*Invitation, error)
	CreateInvitation(ctx context.Context, inv *Invitation) error
	// RespondToInvitation resuelve la invitación y sincroniza la entrada del
	// equipo en la misma transacción: aceptar sin quedar inscrito, o quedar
	// inscrito sin haber aceptado, son estados que no deberían poder existir.
	RespondToInvitation(ctx context.Context, id string, accept bool, at time.Time) (*Invitation, error)
}
