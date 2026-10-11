package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
)

/*
competitionAccess resuelve quién puede leer una competencia y lo que cuelga de
ella: sus partidos y sus entradas.

Vive aparte porque la misma regla la necesitan tres handlers distintos
—competencia, partidos y, si algún día se abre, cobros— y escribirla tres veces
es la forma segura de que las tres terminen diciendo cosas distintas.

La regla es "quién la juega", en dos formas:

	· Del plantel de alguno de los equipos que la juegan (el organizador o
	  cualquiera con entrada). Ve todo.
	· Invitado con convocatoria a un partido DE ESTA competencia. Es la misma
	  llave que en el partido: su vínculo es con lo que vino a jugar, y una
	  membresía de invitado por sí sola no abre nada.

Antes de esto, `GET /competitions/:competitionId` no validaba nada: alcanzaba
con tener sesión y el UUID para leer la competencia de cualquier club, con su
fecha, su cancha y cuánto cuesta.
*/
type competitionAccess struct {
	authz        teamAuthorizer
	competitions competition.Repository
	matches      match.Repository
}

// require devuelve nil si quien pide puede ver la competencia, o el error de
// autorización que le corresponde para que `abortAuthz` lo traduzca.
//
// Devuelve también las entradas, que tuvo que leer para decidir: quien las
// necesita después (`ListEntries`) las reutiliza en vez de volver a pedirlas.
func (a competitionAccess) require(c *gin.Context, comp *competition.Competition) ([]*competition.Entry, error) {
	if _, ok := currentUserID(c); !ok {
		return nil, ErrNoClaims
	}
	ctx := c.Request.Context()

	// Los equipos que la juegan: el que la organiza y los que tienen entrada.
	// La consulta de entradas es una más sobre un índice, y es lo que permite
	// que el rival de un amistoso lea la competencia que no organizó.
	entries, err := a.competitions.ListEntries(ctx, comp.ID)
	if err != nil {
		return nil, err
	}
	// Sin repetidos: el organizador siempre tiene además su propia entrada, y
	// sin este corte su membresía se buscaba dos veces —y cuando no era de ahí,
	// fallaba dos veces— en cada lectura de la competencia.
	teamIDs := []string{comp.OrganizerTeamID}
	seen := map[string]bool{comp.OrganizerTeamID: true}
	for _, entry := range entries {
		if !seen[entry.TeamID] {
			seen[entry.TeamID] = true
			teamIDs = append(teamIDs, entry.TeamID)
		}
	}

	// Se recorren todos los equipos antes de decidir: alguien puede ser
	// invitado de un lado y del plantel del otro, y en ese caso manda el
	// plantel. Cortar en la primera membresía que aparece haría que el orden de
	// las entradas decidiera qué ve.
	var guestMembershipID string
	for _, teamID := range teamIDs {
		m, err := a.authz.requireMembership(c, teamID)
		if err != nil {
			continue
		}
		if !m.IsGuest() {
			return entries, nil
		}
		guestMembershipID = m.ID
	}
	if guestMembershipID == "" {
		return nil, ErrNotMember
	}

	// Invitado: entra solo si alguna de sus convocatorias es a un partido de
	// esta competencia. Se cruzan las dos listas en vez de pedir los convocados
	// de cada partido, que serían tantas consultas como partidos tenga.
	callups, err := a.matches.ListCallupsByMembership(ctx, guestMembershipID)
	if err != nil {
		return nil, err
	}
	if len(callups) == 0 {
		return nil, ErrGuestScope
	}

	matches, err := a.matches.ListByCompetition(ctx, comp.ID)
	if err != nil {
		return nil, err
	}
	belongs := make(map[string]bool, len(matches))
	for _, m := range matches {
		belongs[m.ID] = true
	}
	for _, callup := range callups {
		if belongs[callup.MatchID] {
			return entries, nil
		}
	}
	return nil, ErrGuestScope
}

// requireByID carga la competencia y valida el acceso de una. Devuelve false si
// ya respondió —404 si no existe, 403 si quien pide no la juega—.
func (a competitionAccess) requireByID(c *gin.Context, competitionID string) (*competition.Competition, bool) {
	comp, _, ok := a.requireByIDWithEntries(c, competitionID)
	return comp, ok
}

// requireByIDWithEntries es `requireByID` devolviendo además las entradas que
// leyó para autorizar.
func (a competitionAccess) requireByIDWithEntries(
	c *gin.Context, competitionID string,
) (*competition.Competition, []*competition.Entry, bool) {
	comp, err := a.competitions.FindByID(c.Request.Context(), competitionID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return nil, nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	entries, err := a.require(c, comp)
	if abortAuthz(c, err) {
		return nil, nil, false
	}
	return comp, entries, true
}
