package match

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("match not found")
	ErrCallupNotFound = errors.New("callup not found")
	// ErrNotPlayedYet protege el marcador de un partido que todavía no empezó:
	// no hay resultado que cargar antes de que ruede la pelota.
	ErrNotPlayedYet = errors.New("match has not been played yet")
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
	/*
		El marcador. Nulo es "todavía no lo cargaron", que no es 0 a 0: un
		empate sin goles es un resultado y tiene que poder distinguirse de un
		partido que nadie cerró.

		Los dos van juntos o ninguno —lo garantiza el CHECK de la migración
		005—, así que alcanza con mirar uno para saber si hay resultado. Igual
		se pregunta por `HasResult`, que dice lo que quiere decir.
	*/
	HomeScore *int `json:"home_score,omitempty"`
	AwayScore *int `json:"away_score,omitempty"`
	// Cuándo se cargó el marcador, que no es cuándo se jugó.
	ResultRecordedAt *time.Time `json:"result_recorded_at,omitempty"`
	// Quién lo cargó. El marcador lo declara una persona y nadie lo verifica:
	// sin el autor, un resultado discutido no tiene a quién preguntarle.
	ResultRecordedBy string    `json:"result_recorded_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// Involves indica si el equipo juega este partido.
func (m *Match) Involves(teamID string) bool {
	return m.HomeTeamID == teamID || m.AwayTeamID == teamID
}

// HasResult indica si el marcador ya se cargó.
func (m *Match) HasResult() bool {
	return m.HomeScore != nil && m.AwayScore != nil
}

/*
Result es el marcador que alguien declara, con quién y cuándo lo declaró.

Es un valor aparte y no los campos sueltos del partido porque las cuatro cosas
se escriben juntas: un marcador sin autor no se puede discutir, y un autor sin
fecha no dice si el dato es del rato después del partido o de tres semanas más
tarde.
*/
type Result struct {
	HomeScore  int
	AwayScore  int
	RecordedBy string
	RecordedAt time.Time
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
	/*
		SaveResult guarda el marcador y deja el partido jugado.

		Es un upsert: cargar el resultado de nuevo lo corrige. Un marcador se
		anota de memoria un rato después del partido y equivocarse en un gol es
		normal; obligar a que sea irreversible haría que el error quede para
		siempre, y un resultado que no se puede arreglar es peor que ninguno.

		Escribe el estado `completed` en la misma sentencia, porque son la misma
		cosa dicha dos veces: un partido con marcador es un partido jugado, y
		dejarlos en dos escrituras abre la puerta a que discrepen.
	*/
	SaveResult(ctx context.Context, id string, r Result) (*Match, error)

	ListCallups(ctx context.Context, matchID string) ([]*Callup, error)
	ListCallupsByMembership(ctx context.Context, membershipID string) ([]*Callup, error)
	// CallUp es idempotente: a quien ya fue convocado no se le borra la
	// respuesta. Tocar "Convocar a todos" no puede deshacer los "voy" recibidos.
	CallUp(ctx context.Context, matchID string, membershipIDs []string) ([]*Callup, error)
	Respond(ctx context.Context, matchID, membershipID string, attending bool, at time.Time) (*Callup, error)
}
