package funds

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("funds entry not found")

type SourceType string

const (
	SourceMatchCost SourceType = "match_cost"
)

// Source es de dónde sale el fondo: el costo de un partido. Mismo par
// (tipo, id) que usa charges para apuntar al mismo lugar.
type Source struct {
	Type SourceType `json:"type"`
	ID   string     `json:"id"`
}

/*
Entry es lo que un partido ya jugado dejó para el equipo: lo que se le cobró a
los confirmados menos lo que al equipo le toca poner del lugar. Uno por partido.

Un monto negativo significa que el equipo absorbió la diferencia: fueron menos
que la nómina y la cancha se pagó igual.

No se guarda, se deriva. Mientras existió el reparto del manager, el fondo se
escribía en `team_funds` al repartir, y un partido que nadie repartió no dejaba
fondo aunque hubiera sobrado plata. Desde que el cobro nace al confirmar, los
cobros ya son el dato: calcularlo desde ahí no se desactualiza nunca.
*/
type Entry struct {
	TeamID string `json:"team_id"`
	Source Source `json:"source"`
	// SourceName es el nombre de la competencia de la que salió el fondo. Lo
	// resuelve el repositorio en la misma consulta y no el handler pidiendo una
	// competencia por entrada: con treinta partidos eso eran treinta viajes de
	// ida y vuelta a la base para pintar treinta líneas de texto.
	//
	// Vacío si el origen ya no existe.
	SourceName string `json:"source_name,omitempty"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	// UpdatedAt es cuándo se jugó el partido. Conserva el nombre del campo
	// que la app ya lee.
	UpdatedAt time.Time `json:"updated_at"`
}

type Repository interface {
	// ListByTeam devuelve los fondos del equipo, uno por partido jugado, el
	// más reciente primero. Un partido que quedó justo no aparece.
	ListByTeam(ctx context.Context, teamID string) ([]*Entry, error)
}
