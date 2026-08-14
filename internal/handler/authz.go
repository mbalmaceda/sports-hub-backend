package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
)

// Errores de autorización, separados de los de negocio para que el handler
// pueda mapearlos a 401/403 sin adivinar.
var (
	ErrNoClaims     = errors.New("unauthorized")
	ErrNotMember    = errors.New("you are not a member of this team")
	ErrInsufficient = errors.New("your role does not allow this action")
	// ErrGuestScope es el invitado de un partido pidiendo algo del equipo.
	// Tiene membresía, pero su vínculo es con un partido y no con el club: no
	// ve el plantel, ni las finanzas, ni los otros partidos.
	ErrGuestScope = errors.New("your invitation only covers the match you were called up to")
)

// teamAuthorizer resuelve qué puede hacer el usuario del token dentro de un equipo.
//
// El middleware de auth solo prueba que el JWT es válido; no dice nada de a qué
// equipos pertenece quien lo trae. Sin este paso, cualquier usuario autenticado
// podría aceptar un amistoso en nombre de otro club o convocar a jugadores
// ajenos, que es justo lo que los endpoints de competencias no pueden permitir.
type teamAuthorizer struct {
	memberships membership.Repository
}

// requireMember exige ser del equipo, con cualquier rol.
//
// Excluye a los invitados de un partido: tienen membresía para que la
// convocatoria y el cobro funcionen, pero no son del club. Este es el guard por
// defecto y el que hay que usar salvo que el endpoint sea justamente uno de los
// que un invitado tiene que poder tocar —su partido, su cargo—, y en ese caso
// va `requireMembership` y quien la llama acota el alcance.
func (a teamAuthorizer) requireMember(c *gin.Context, teamID string) (*membership.Membership, error) {
	m, err := a.requireMembership(c, teamID)
	if err != nil {
		return nil, err
	}
	if m.IsGuest() {
		return nil, ErrGuestScope
	}
	return m, nil
}

// requireMembership exige tener membresía activa en el equipo, contando a los
// invitados. Devuelve la membresía para que el handler no tenga que volver a
// buscarla.
//
// Quien la usa se hace cargo de acotar al invitado: un invitado no tiene
// equipo, tiene un partido, y esta función sola no sabe cuál.
func (a teamAuthorizer) requireMembership(c *gin.Context, teamID string) (*membership.Membership, error) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		return nil, ErrNoClaims
	}

	m, err := a.memberships.FindByUserAndTeam(c.Request.Context(), claims.UserID, teamID)
	if errors.Is(err, membership.ErrNotFound) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	// Una membresía dada de baja no habilita nada: sigue existiendo la fila,
	// pero la persona ya no está en el equipo.
	if m.Status != membership.StatusActive {
		return nil, ErrNotMember
	}
	return m, nil
}

// requireRole exige pertenecer al equipo con alguno de los roles dados.
func (a teamAuthorizer) requireRole(
	c *gin.Context, teamID string, allowed ...membership.Role,
) (*membership.Membership, error) {
	m, err := a.requireMember(c, teamID)
	if err != nil {
		return nil, err
	}
	for _, role := range allowed {
		if m.Role == role {
			return m, nil
		}
	}
	return nil, ErrInsufficient
}

// abortAuthz traduce el error de autorización a la respuesta HTTP y corta.
// Devuelve true si efectivamente cortó, para usarlo como guarda en una línea.
func abortAuthz(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrNoClaims):
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
	case errors.Is(err, ErrNotMember):
		// 403 y no 404: el equipo existe, lo que falta es permiso. Devolver 404
		// escondería el recurso, pero acá el id del equipo ya lo trae el cliente.
		c.AbortWithStatusJSON(403, gin.H{"error": ErrNotMember.Error()})
	case errors.Is(err, ErrInsufficient):
		c.AbortWithStatusJSON(403, gin.H{"error": ErrInsufficient.Error()})
	case errors.Is(err, ErrGuestScope):
		c.AbortWithStatusJSON(403, gin.H{"error": ErrGuestScope.Error()})
	default:
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
	}
	return true
}

// currentUserID es azúcar para los handlers que solo necesitan el id del token.
func currentUserID(c *gin.Context) (string, bool) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		return "", false
	}
	return claims.UserID, true
}
