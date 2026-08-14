// Package guest modela la invitación a un partido de alguien que no es del
// equipo: el "parche" que completa una convocatoria.
//
// Es el tercer camino de incorporación, y no vive en `onboarding` con los otros
// dos por una diferencia de fondo: team_invitations y join_requests apuntan a
// un usuario que ya existe (`user_id NOT NULL`), y esta invitación existe
// ANTES que la persona. Lo que identifica al invitado es el token, no su
// cuenta, porque todavía no tiene una.
//
// Y es a un partido, no al equipo: quien lo canjea entra a jugar ese sábado, no
// a formar parte del club.
package guest

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("invitation not found")
	// ErrNotUsable cubre las tres formas de que un enlace no sirva —vencido,
	// revocado o sin cupo— con un solo error a propósito: al que lo recibe hay
	// que decirle que el enlace ya no vale, y cuál de las tres fue no le
	// cambia nada y sí le cuenta de más a quien esté probando tokens.
	ErrNotUsable = errors.New("this invitation link is no longer valid")
	// ErrAlreadyMember es el que llega cuando alguien del plantel abre el
	// enlace del grupo. No es un error del enlace: ya está adentro.
	ErrAlreadyMember = errors.New("you already belong to this team")
)

// Invite es el enlace que el manager comparte.
//
// El token en claro no está acá: se genera, se devuelve una sola vez al
// crearlo y de ahí en más solo existe su hash. Si el manager lo pierde, se hace
// otro.
type Invite struct {
	ID              string
	MatchID         string
	TeamID          string
	CreatedByUserID string
	MaxUses         int
	UsedCount       int
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	CreatedAt       time.Time
}

// Usable indica si el enlace todavía admite a alguien.
//
// Las tres condiciones son la misma pregunta desde tres lados: que no lo hayan
// apagado, que el partido no haya empezado y que quede lugar.
func (i *Invite) Usable(now time.Time) bool {
	return i.RevokedAt == nil && now.Before(i.ExpiresAt) && i.UsedCount < i.MaxUses
}

// RemainingUses es cuántos lugares quedan. Nunca negativo.
func (i *Invite) RemainingUses() int {
	if i.UsedCount >= i.MaxUses {
		return 0
	}
	return i.MaxUses - i.UsedCount
}

// Preview es lo que ve quien abre el enlace sin haber entrado todavía.
//
// Es deliberadamente flaco: nombre del equipo, cuándo y dónde se juega, quién
// invita y cuánto sale. Nada de plantel, nada de finanzas del equipo, ningún
// dato de contacto de nadie. Es una URL pública —cualquiera que tenga el token
// la lee— así que lo que se pone acá se considera publicado.
//
// El costo va incluido porque el que llega tiene que saber a qué se está
// comprometiendo: enterarse en la cancha de que debe la cuota es la clase de
// sorpresa que quema al equipo que invitó.
type Preview struct {
	TeamName     string    `json:"team_name"`
	InvitedBy    string    `json:"invited_by"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	Venue        string    `json:"venue,omitempty"`
	IsInternal   bool      `json:"is_internal"`
	OpponentName string    `json:"opponent_name,omitempty"`
	// Cuota por jugador, en unidades menores de la moneda. Cero si el partido
	// no tiene costo de cancha cargado.
	CostPerPlayer int64     `json:"cost_per_player"`
	Currency      string    `json:"currency,omitempty"`
	RemainingUses int       `json:"remaining_uses"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// AcceptResult es lo que quedó creado al canjear el enlace.
type AcceptResult struct {
	MembershipID string `json:"membership_id"`
	TeamID       string `json:"team_id"`
	MatchID      string `json:"match_id"`
}

type Repository interface {
	Create(ctx context.Context, inv *Invite, tokenHash string) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*Invite, error)
	FindByID(ctx context.Context, id string) (*Invite, error)
	ListByMatch(ctx context.Context, matchID string) ([]*Invite, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	/*
		Redeem canjea el enlace en una sola transacción: descuenta el cupo, crea
		la membresía de invitado y deja la convocatoria confirmada.

		Va junto y no en tres pasos porque cada corte en el medio deja un
		destrozo distinto: cupo consumido sin nadie adentro, o alguien adentro
		del equipo sin convocatoria al partido por el que entró —y esa membresía
		huérfana es justamente la que después cobra cuotas.

		El descuento del cupo es condicional dentro de la misma transacción, así
		que dos personas tocando el enlace a la vez no pueden pasarse del
		máximo. Si ya no queda lugar devuelve ErrNotUsable.

		Es idempotente para quien ya está en el equipo: devuelve ErrAlreadyMember
		sin consumir cupo, porque el del plantel que abre el enlace del grupo no
		tiene que gastarle un lugar a nadie.
	*/
	Redeem(ctx context.Context, tokenHash, userID string, now time.Time) (*AcceptResult, error)
}
