package settlement

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("settlement not found")
	// ErrAlreadyPaid frena declarar dos veces la misma transferencia, por
	// ejemplo desde una pantalla vieja que quedó abierta en el teléfono.
	ErrAlreadyPaid = errors.New("this settlement was already paid")
)

type SourceType string

const SourceMatchCost SourceType = "match_cost"

// Source es de dónde sale la deuda. Mismo par (tipo, id) que charges: para
// 'match_cost' el id es el de la competencia, porque el costo del lugar es de
// la competencia y no de cada partido.
type Source struct {
	Type SourceType `json:"type"`
	ID   string     `json:"id"`
}

type Status string

const (
	StatusPending Status = "pending"
	StatusPaid    Status = "paid"
)

/*
Settlement es lo que un equipo le debe a otro por el lugar donde jugaron.

La cancha la reserva y la paga el organizador. El rival cobra su mitad a sus
propios jugadores —eso ya funcionaba— pero esa plata se quedaba en su cuenta:
faltaba el tramo que la lleva a quien puso los $28.000. Este es ese tramo, y es
una sola transferencia entre managers en vez de catorce.

El deudor es un equipo y no una persona, y por eso esto no es un `charge`. Lo
paga quien maneja la plata de ese equipo, sea el manager o el tesorero.
*/
type Settlement struct {
	ID         string     `json:"id"`
	Source     Source     `json:"source"`
	FromTeamID string     `json:"from_team_id"`
	ToTeamID   string     `json:"to_team_id"`
	Amount     int64      `json:"amount"`
	Currency   string     `json:"currency"`
	Status     Status     `json:"status"`
	PaidAt     *time.Time `json:"paid_at,omitempty"`
	// Quién declaró la transferencia. Nadie la verifica del otro lado, así que
	// sin el autor un pago discutido no tiene a quién preguntarle.
	PaidBy    string    `json:"paid_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Involves indica si el equipo es una de las dos puntas.
func (s *Settlement) Involves(teamID string) bool {
	return s.FromTeamID == teamID || s.ToTeamID == teamID
}

// IsDebtor indica si a este equipo le toca transferir. Es la pregunta que
// decide si la app le muestra el aviso: el que cobra no tiene nada que hacer.
func (s *Settlement) IsDebtor(teamID string) bool {
	return s.FromTeamID == teamID
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Settlement, error)
	// FindBySource devuelve la deuda de una competencia, si la hay. Que no haya
	// es normal: una cancha gratis o un partido interno no generan ninguna.
	FindBySource(ctx context.Context, source Source) (*Settlement, error)
	// ListByTeam devuelve las dos direcciones: lo que el equipo debe y lo que
	// le deben. Separarlas en dos consultas obligaría a la app a pedir dos
	// veces para contestar "¿cómo estoy?".
	ListByTeam(ctx context.Context, teamID string) ([]*Settlement, error)
	/*
		Create deja la deuda anotada. Es idempotente por el UNIQUE del origen:
		si ya existe, devuelve la que está en vez de fallar.

		Nace al aceptar el amistoso, que es cuando el compromiso existe y
		aparece el rival. Antes de eso no hay a quién cobrarle: el desafío
		todavía puede quedar sin respuesta.
	*/
	Create(ctx context.Context, s *Settlement) (*Settlement, error)
	/*
		MarkPaid cierra la deuda con la declaración del que transfirió.

		Un solo paso, igual que el comprobante de un cobro: el que paga declara
		y se le cree. El control de dos ojos costaba más de lo que cuidaba entre
		dos managers que acaban de jugar juntos.
	*/
	MarkPaid(ctx context.Context, id, paidBy string, at time.Time) (*Settlement, error)
}
