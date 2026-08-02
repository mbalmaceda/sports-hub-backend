package match

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("match not found")
	ErrCallupNotFound = errors.New("callup not found")
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusConfirmed Status = "confirmed"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

type Match struct {
	ID            string    `json:"id"`
	CompetitionID string    `json:"competition_id"`
	HomeTeamID    string    `json:"home_team_id"`
	AwayTeamID    string    `json:"away_team_id"`
	ScheduledAt   time.Time `json:"scheduled_at"`
	Venue         string    `json:"venue,omitempty"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// Involves indica si el equipo juega este partido.
func (m *Match) Involves(teamID string) bool {
	return m.HomeTeamID == teamID || m.AwayTeamID == teamID
}

// CallupStatus solo tiene tres valores: "sin convocar" es la ausencia de fila,
// no un estado guardado. Así no puede desincronizarse del plantel real.
type CallupStatus string

const (
	CallupCalled    CallupStatus = "called"
	CallupConfirmed CallupStatus = "confirmed"
	CallupDeclined  CallupStatus = "declined"
)

type Callup struct {
	ID           string       `json:"id"`
	MatchID      string       `json:"match_id"`
	MembershipID string       `json:"membership_id"`
	Status       CallupStatus `json:"status"`
	CalledAt     time.Time    `json:"called_at"`
	RespondedAt  *time.Time   `json:"responded_at,omitempty"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Match, error)
	ListByCompetition(ctx context.Context, competitionID string) ([]*Match, error)
	ListByTeam(ctx context.Context, teamID string) ([]*Match, error)
	// ListByTeamOnDate sirve para avisar choques de calendario al agendar.
	ListByTeamOnDate(ctx context.Context, teamID string, day time.Time) ([]*Match, error)
	Create(ctx context.Context, m *Match) error
	UpdateStatus(ctx context.Context, id string, status Status) error

	ListCallups(ctx context.Context, matchID string) ([]*Callup, error)
	ListCallupsByMembership(ctx context.Context, membershipID string) ([]*Callup, error)
	// CallUp es idempotente: a quien ya fue convocado no se le borra la
	// respuesta. Tocar "Convocar a todos" no puede deshacer los "voy" recibidos.
	CallUp(ctx context.Context, matchID string, membershipIDs []string) ([]*Callup, error)
	Respond(ctx context.Context, matchID, membershipID string, attending bool, at time.Time) (*Callup, error)
}
