package friendly

import (
	"context"
	"errors"
	"time"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
)

var (
	ErrSearchNotFound = errors.New("friendly search not found")
	// ErrSearchClosed cubre pedir una búsqueda que ya encontró rival, que se
	// canceló o cuyo partido ya pasó.
	ErrSearchClosed = errors.New("friendly search is no longer open")
	// ErrAlreadyRequested es el índice único de la 013: un equipo tiene a lo
	// más un pedido abierto por búsqueda.
	ErrAlreadyRequested = errors.New("your team already requested this friendly")
)

type SearchStatus string

const (
	SearchOpen      SearchStatus = "open"
	SearchMatched   SearchStatus = "matched"
	SearchCancelled SearchStatus = "cancelled"
	SearchExpired   SearchStatus = "expired"
)

// MaxSearchRadiusKm es el tope del radio que acepta el listado. La app ofrece
// 2, 5 y 10 km; más que eso deja de ser "cerca" y el bounding box empieza a
// barrer media región.
const MaxSearchRadiusKm = 10.0

/*
SearchTTL es cuánto vive una búsqueda publicada, contado desde que se crea.

No se cuenta hacia atrás desde el partido: una publicación para dentro de tres
semanas no puede quedarse tres semanas en el mapa, porque a esa altura nadie la
está mirando y el que la pide espera una respuesta que no llega. Igual que en
desafíos e invitaciones, el plazo nunca pasa de la hora del partido
(`responseDeadline`).
*/
const SearchTTL = 24 * time.Hour

/*
Search es un amistoso publicado sin rival ("buscar rival").

Fecha, cancha y costo ya están resueltos; lo que falta es con quién. Cada equipo
que lo pide abre un desafío normal (`Challenge.SearchID`), y el publicador elige
cuál aceptar. Aceptar uno cierra la búsqueda y rechaza los demás.

La regla que hay que sostener en todo lo que cuelga de acá: **el retador es el
publicador**, aunque haya sido el otro el que tocó "Solicitar". Quien publica es
quien reservó la cancha, y en este modelo el retador es el organizador —el que
paga el lugar y le cobra la mitad al rival en `ensureVenueSettlement`—.
Invertirlo deja la liquidación al revés: el que no puso nada cobrando la mitad.
*/
type Search struct {
	ID             string                 `json:"id"`
	TeamID         string                 `json:"team_id"`
	TeamName       string                 `json:"team_name,omitempty"`
	SportID        string                 `json:"sport_id"`
	PlayersPerSide *int                   `json:"players_per_side,omitempty"`
	StartAt        time.Time              `json:"start_at"`
	Venue          venue.Venue            `json:"venue"`
	VenueCost      *competition.VenueCost `json:"venue_cost,omitempty"`
	Message        string                 `json:"message,omitempty"`
	Status         SearchStatus           `json:"status"`
	CreatedBy      string                 `json:"-"`
	CreatedAt      time.Time              `json:"created_at"`
	// ExpiresAt es `CreatedAt + SearchTTL`, acotado a `StartAt`.
	ExpiresAt time.Time `json:"expires_at"`

	// DistanceKm es desde el punto con que se pidió el listado.
	DistanceKm *float64 `json:"distance_km,omitempty"`
	// RequestedChallengeID es el pedido abierto del equipo que mira, si lo
	// tiene. Con eso la app sabe si mostrar "Solicitar" o "Solicitud enviada"
	// sin pedir los desafíos aparte.
	RequestedChallengeID string `json:"requested_challenge_id,omitempty"`
}

// IsOpen dice si la búsqueda todavía admite pedidos. El plazo manda igual que
// el estado: una búsqueda vencida que la barrida todavía no tocó sigue
// diciendo 'open' en Postgres. `ExpiresAt` nunca pasa de `StartAt`, así que
// cubre también el partido que ya empezó.
func (s *Search) IsOpen(now time.Time) bool {
	return s.Status == SearchOpen && s.ExpiresAt.After(now)
}

type SearchRepository interface {
	Create(ctx context.Context, s *Search) error
	FindByID(ctx context.Context, id string) (*Search, error)
	/*
		ListNearby devuelve las búsquedas abiertas cuya cancha cae a menos de
		`radiusKm` del punto, de la más cercana a la más lejana.

		`viewerTeamID` llena `RequestedChallengeID`. Filtra por hora además de
		por estado: lo vencido no se muestra aunque nadie lo haya barrido.
	*/
	ListNearby(ctx context.Context, lat, lng, radiusKm float64, viewerTeamID string, now time.Time) ([]*Search, error)
	/*
		Close deja la búsqueda en `status` y rechaza los pedidos abiertos, salvo
		`keepChallengeID` (el aceptado, si lo hay), en una transacción.

		Devuelve los desafíos rechazados para que quien llama cancele sus
		competencias y avise: el repositorio de amistosos no toca competencias,
		igual que en `ExpireStale`.

		Si la búsqueda ya no estaba abierta devuelve ErrSearchClosed y no toca
		nada: aceptar dos pedidos al mismo tiempo no puede cerrar la búsqueda
		dos veces.
	*/
	Close(ctx context.Context, id string, status SearchStatus, keepChallengeID string) ([]*Challenge, error)
	// ExpireStale vence las búsquedas abiertas cuyo plazo (`ExpiresAt`) ya pasó.
	ExpireStale(ctx context.Context, now time.Time) error
}
