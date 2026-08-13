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

// Source es de dónde sale el fondo: el reparto del costo de un partido. Mismo
// par (tipo, id) que usa charges para apuntar al mismo lugar.
type Source struct {
	Type SourceType `json:"type"`
	ID   string     `json:"id"`
}

// Entry es el excedente que un reparto dejó para el equipo. Se sobrescribe cada
// vez que se rehace el reparto de ese origen, así que nunca hay dos filas del
// mismo partido. Un monto negativo significa que el equipo absorbió la
// diferencia entre lo recaudado y su mitad del lugar.
type Entry struct {
	TeamID string `json:"team_id"`
	Source Source `json:"source"`
	// SourceName es el nombre de la competencia de la que salió el fondo. Lo
	// resuelve el repositorio en la misma consulta y no el handler pidiendo una
	// competencia por entrada: con treinta partidos eso eran treinta viajes de
	// ida y vuelta a la base para pintar treinta líneas de texto.
	//
	// Vacío si el origen ya no existe.
	SourceName string    `json:"source_name,omitempty"`
	Amount     int64     `json:"amount"`
	Currency   string    `json:"currency"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Repository interface {
	// Set registra el fondo que deja un reparto para ese origen. Un monto cero
	// borra la entrada: si el partido no deja plata, no queda registro.
	Set(ctx context.Context, teamID string, source Source, amount int64, currency string) error
	// ListByTeam devuelve los fondos del equipo, uno por origen, actualizados
	// primero.
	ListByTeam(ctx context.Context, teamID string) ([]*Entry, error)
}
