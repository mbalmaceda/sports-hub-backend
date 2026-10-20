// Package teamlink modela el enlace para unirse a un equipo: el que el manager
// tira al grupo de WhatsApp para que se sumen los que todavía no tienen la app.
//
// Es primo de `guest` —token al portador, cupo, vencimiento— y no vive ahí por
// lo que crea al canjearse. El enlace de un parche da una membresía de
// invitado encerrada en un partido; este da una de plantel, la misma que deja
// aceptar una invitación por correo. Tampoco vive en `onboarding`, donde están
// esas invitaciones, porque allá todo apunta a un usuario que ya existe y este
// enlace existe antes que la persona.
package teamlink

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("team link not found")
	// ErrNotUsable cubre vencido, anulado y sin cupo con un solo error, por lo
	// mismo que `guest.ErrNotUsable`: al que llegó tarde hay que decirle que el
	// enlace ya no vale, y cuál de las tres fue solo le cuenta de más a quien
	// esté probando tokens.
	ErrNotUsable = errors.New("this team link is no longer valid")
	// ErrAlreadyMember es el del plantel que abre el enlace del grupo. No es un
	// error del enlace: ya está adentro, y no le gasta un lugar a nadie.
	ErrAlreadyMember = errors.New("you already belong to this team")
)

// Invite es el enlace que el manager comparte. Como en `guest`, el token en
// claro no está acá: se devuelve una sola vez al crearlo.
type Invite struct {
	ID              string
	TeamID          string
	CreatedByUserID string
	MaxUses         int
	UsedCount       int
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	CreatedAt       time.Time
}

// Usable indica si el enlace todavía admite a alguien: que no lo hayan
// apagado, que no haya vencido y que quede lugar.
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

// Preview es lo que ve quien abre el enlace sin haber entrado.
//
// Es una URL pública —cualquiera con el token la lee— así que lleva solo lo que
// el equipo ya muestra de sí mismo al resto: nombre, deporte, ciudad, su
// descripción y quién invita. Nada de plantel, finanzas ni contactos.
type Preview struct {
	TeamName      string    `json:"team_name"`
	SportID       string    `json:"sport_id"`
	City          string    `json:"city,omitempty"`
	Description   string    `json:"description,omitempty"`
	InvitedBy     string    `json:"invited_by"`
	RemainingUses int       `json:"remaining_uses"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// AcceptResult es lo que quedó al canjear el enlace.
type AcceptResult struct {
	MembershipID string `json:"membership_id"`
	TeamID       string `json:"team_id"`
}

type Repository interface {
	Create(ctx context.Context, inv *Invite, tokenHash string) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*Invite, error)
	FindByID(ctx context.Context, id string) (*Invite, error)
	ListByTeam(ctx context.Context, teamID string) ([]*Invite, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	/*
		Redeem canjea el enlace en una transacción: descuenta el cupo y da de alta
		la membresía de plantel.

		Quien ya es del club (activo o suspendido) recibe ErrAlreadyMember sin
		gastar cupo: el que abre el enlace del grupo no tiene que quitarle un
		lugar a nadie, y el suspendido no se reactiva solo por tener el link. El
		parche de este equipo y el que se había ido sí entran, sobre su misma
		fila (`UNIQUE (user_id, team_id)`).
	*/
	Redeem(ctx context.Context, tokenHash, userID string, now time.Time) (*AcceptResult, error)
}
