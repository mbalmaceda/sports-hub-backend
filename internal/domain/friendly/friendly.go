package friendly

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("friendly challenge not found")
	// ErrClosed cubre responder un desafío ya aceptado, rechazado o expirado.
	ErrClosed = errors.New("friendly challenge is no longer open")
	// ErrNotYourTurn evita que un equipo acepte su propia propuesta. Sin este
	// chequeo, cualquiera cerraría un partido sin que el rival diga que sí.
	ErrNotYourTurn = errors.New("waiting for the other team to respond")
	ErrExpired     = errors.New("friendly challenge has expired")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusCountered Status = "countered"
	StatusAccepted  Status = "accepted"
	StatusDeclined  Status = "declined"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

// IsOpen indica si el desafío todavía admite respuesta.
func (s Status) IsOpen() bool {
	return s == StatusPending || s == StatusCountered
}

type Challenge struct {
	ID               string    `json:"id"`
	CompetitionID    string    `json:"competition_id"`
	ChallengerTeamID string    `json:"challenger_team_id"`
	ChallengedTeamID string    `json:"challenged_team_id"`
	Status           Status    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`

	// Quién hizo la última propuesta. Define de quién es el turno: si la hizo
	// el otro equipo, nos toca responder. La trae el listado para que el
	// cliente pueda distinguir una contraoferta sin respuesta de una ya
	// contestada, sin tener que pedir las propuestas una por una.
	LastProposedByTeamID string `json:"last_proposed_by_team_id,omitempty"`
}

// Opponent devuelve el otro equipo del desafío.
func (c *Challenge) Opponent(teamID string) string {
	if c.ChallengerTeamID == teamID {
		return c.ChallengedTeamID
	}
	return c.ChallengerTeamID
}

// Involves indica si el equipo es parte del desafío.
func (c *Challenge) Involves(teamID string) bool {
	return c.ChallengerTeamID == teamID || c.ChallengedTeamID == teamID
}

type Proposal struct {
	ID               string    `json:"id"`
	ChallengeID      string    `json:"challenge_id"`
	ProposedByTeamID string    `json:"proposed_by_team_id"`
	ProposedStartAt  time.Time `json:"proposed_start_at"`
	ProposedVenue    string    `json:"proposed_venue,omitempty"`
	Message          string    `json:"message,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Challenge, error)
	ListByTeam(ctx context.Context, teamID string) ([]*Challenge, error)
	// Create inserta el desafío junto con su primera propuesta: un desafío sin
	// propuesta no tiene fecha ni lugar, o sea que no es nada.
	Create(ctx context.Context, ch *Challenge, first *Proposal) error
	UpdateStatus(ctx context.Context, id string, status Status) error

	ListProposals(ctx context.Context, challengeID string) ([]*Proposal, error)
	LatestProposal(ctx context.Context, challengeID string) (*Proposal, error)
	// AddProposal guarda la contraoferta y deja el desafío en 'countered'.
	AddProposal(ctx context.Context, p *Proposal) error
}
