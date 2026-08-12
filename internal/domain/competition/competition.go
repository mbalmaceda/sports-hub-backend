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
	/*
		PlayerShare es cuánto le toca poner a cada jugador, en la moneda de
		VenueCost. Se calcula una sola vez, al crear la competencia, y desde ahí
		se lee: es el número que el asistente le prometió al manager, y
		derivarlo en cada lectura dejaría que un cambio de fórmula alterara en
		silencio lo que ya se le cobró a la gente.

		Nulo cuando no hay costo de lugar o no se configuró la nómina.
	*/
	PlayerShare *int64 `json:"player_share,omitempty"`
	/*
		IsInternal marca el partido interno: el equipo juega contra sí mismo y
		pone los jugadores de los dos lados.

		No hay rival, ni desafío, ni nada que negociar —el partido nace
		confirmado—, y la nómina a convocar es el doble: los que entran por lado
		por los dos lados. Lo demás es igual a cualquier amistoso, por eso es una
		bandera y no un tipo nuevo de competencia.
	*/
	IsInternal bool      `json:"is_internal"`
	CreatedAt  time.Time `json:"created_at"`
}

// ResolvePlayerShare es la cuota por jugador: el lugar repartido entre los que
// entran en cancha por los dos lados. Divide hacia arriba para que la suma de
// las partes nunca quede corta contra el costo real.
//
// Se usa solo al crear: después, el valor vive en PlayerShare.
func (c *Competition) ResolvePlayerShare() *int64 {
	if c.VenueCost == nil || c.VenueCost.Amount <= 0 || c.PlayersPerSide == nil || *c.PlayersPerSide <= 0 {
		return nil
	}
	parts := int64(*c.PlayersPerSide) * 2
	share := (c.VenueCost.Amount + parts - 1) / parts
	return &share
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
	// ListByTeam devuelve las competencias en las que el equipo es parte: las que
	// organiza, las que tiene una entrada, o las que le llegaron como desafío o
	// invitación todavía sin responder.
	ListByTeam(ctx context.Context, teamID string) ([]*Competition, error)
	Create(ctx context.Context, c *Competition) error
	UpdateStatus(ctx context.Context, id string, status Status) error
	/*
		UpdateSchedule fija la fecha y el lugar acordados.

		La competencia nace con la primera propuesta del amistoso, pero recién
		al aceptar se sabe cuál quedó: con una contraoferta de por medio, la
		original ya no es la buena. Sin esto, `start_at` queda con una fecha que
		nadie va a jugar, y el móvil la usa para decidir qué es una competencia
		activa y qué ya pasó.
	*/
	UpdateSchedule(ctx context.Context, id string, startAt time.Time, venue string) error

	ListEntries(ctx context.Context, competitionID string) ([]*Entry, error)
	// UpsertEntry crea la entrada o actualiza su estado si el equipo ya estaba.
	UpsertEntry(ctx context.Context, e *Entry) error

	FindInvitation(ctx context.Context, id string) (*Invitation, error)
	ListInvitationsForTeam(ctx context.Context, teamID string) ([]*Invitation, error)
	// ExpireStaleInvitations marca vencidas las invitaciones sin responder cuyo
	// plazo pasó. A diferencia del amistoso, acá no se cancela nada: el torneo
	// sigue para los demás, el que se quedó afuera es el equipo que no contestó.
	// Además libera el índice único de invitaciones 'sent', que es lo que
	// permite volver a invitar al mismo equipo.
	ExpireStaleInvitations(ctx context.Context, now time.Time) error
	CreateInvitation(ctx context.Context, inv *Invitation) error
	// RespondToInvitation resuelve la invitación y sincroniza la entrada del
	// equipo en la misma transacción: aceptar sin quedar inscrito, o quedar
	// inscrito sin haber aceptado, son estados que no deberían poder existir.
	RespondToInvitation(ctx context.Context, id string, accept bool, at time.Time) (*Invitation, error)
}
