package auth

import (
	"context"
	"errors"
	"time"
)

var ErrTokenNotFound = errors.New("refresh token not found")

const (
	// RefreshTokenTTL es lo que dura una sesión sin volver a escribir la
	// contraseña. Se renueva en cada rotación, así que quien usa la app seguido
	// no se desloguea nunca.
	RefreshTokenTTL = 30 * 24 * time.Hour

	// ReuseGrace es la ventana en la que un token ya rotado se acepta de nuevo
	// sin tratarlo como robo.
	//
	// Existe por un caso real, no por tolerancia: cuando la app tiene varias
	// requests en vuelo y el access token vence, todas reciben 401 y varias
	// disparan el refresh a la vez. Sin esta ventana, la segunda se vería
	// idéntica a una reutilización maliciosa y desloguearía a un usuario
	// legítimo cada vez que abre la app con mala señal.
	ReuseGrace = 30 * time.Second

	// MaxSessionsPerUser acota cuántas cadenas de sesión vivas puede tener una
	// persona. Sin tope, cada login deja una familia para siempre.
	MaxSessionsPerUser = 10
)

// RefreshToken es un eslabón de una cadena de sesión.
//
// FamilyID es lo que une los eslabones: al rotar, el token nuevo hereda la
// familia del viejo. Eso permite matar una sesión entera —todos los tokens que
// descienden de un mismo login— cuando se detecta que alguien reutilizó uno.
type RefreshToken struct {
	ID        string
	UserID    string
	FamilyID  string
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
	// UsedAt se marca al rotar. Un token con UsedAt ya cumplió su función: si
	// vuelve a aparecer pasada la gracia, o lo robaron o se filtró.
	UsedAt *time.Time
	// RevokedAt lo marca la revocación de toda la familia.
	RevokedAt *time.Time
}

// Usable dice si el token sirve para pedir uno nuevo. Un token usado no es
// usable, pero tampoco es necesariamente un ataque: eso lo decide la gracia.
func (t *RefreshToken) Usable(now time.Time) bool {
	return t.RevokedAt == nil && t.UsedAt == nil && now.Before(t.ExpiresAt)
}

// WithinGrace distingue el refresh concurrente legítimo de la reutilización.
func (t *RefreshToken) WithinGrace(now time.Time) bool {
	return t.UsedAt != nil && now.Sub(*t.UsedAt) <= ReuseGrace
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, t *RefreshToken) error
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// MarkUsed marca el token como rotado. Devuelve ErrTokenNotFound si otra
	// request ganó la carrera y ya lo había marcado, para que dos refresh
	// simultáneos no emitan dos veces.
	MarkUsed(ctx context.Context, id string, at time.Time) error
	// RevokeFamily mata la cadena entera. Es lo que se hace al detectar una
	// reutilización y también al cerrar sesión en un dispositivo.
	RevokeFamily(ctx context.Context, familyID string, at time.Time) error
	// RevokeAllForUser cierra todas las sesiones de la persona.
	RevokeAllForUser(ctx context.Context, userID string, at time.Time) error
	// DeleteExpired lo llama el reaper: sin él la tabla crece una fila por
	// login para siempre.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
	// TrimFamilies deja vivas solo las `keep` familias más recientes.
	TrimFamilies(ctx context.Context, userID string, keep int) error
}
