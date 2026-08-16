package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

type MatchHandler struct {
	matches       match.Repository
	memberships   membership.Repository
	competitions  competition.Repository
	charges       charge.Repository
	notifications *notification.Service
	firebase      *firebase.Firebase
	authz         teamAuthorizer
	access        competitionAccess
}

func NewMatchHandler(
	matches match.Repository,
	memberships membership.Repository,
	competitions competition.Repository,
	charges charge.Repository,
	notifications *notification.Service,
	fb *firebase.Firebase,
) *MatchHandler {
	return &MatchHandler{
		matches:       matches,
		memberships:   memberships,
		competitions:  competitions,
		charges:       charges,
		notifications: notifications,
		firebase:      fb,
		authz:         teamAuthorizer{memberships: memberships},
		access: competitionAccess{
			authz:        teamAuthorizer{memberships: memberships},
			competitions: competitions,
			matches:      matches,
		},
	}
}

// ListByTeam GET /teams/:id/matches
func (h *MatchHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	items, err := h.matches.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list matches"})
		return
	}
	if items == nil {
		items = []*match.Match{}
	}
	c.JSON(http.StatusOK, items)
}

// ListByCompetition GET /competitions/:competitionId/matches
//
// Mismo alcance que la competencia: la lee quien la juega. Ver
// `competitionAccess`.
func (h *MatchHandler) ListByCompetition(c *gin.Context) {
	if _, ok := h.access.requireByID(c, c.Param("competitionId")); !ok {
		return
	}

	items, err := h.matches.ListByCompetition(c.Request.Context(), c.Param("competitionId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list matches"})
		return
	}
	if items == nil {
		items = []*match.Match{}
	}
	c.JSON(http.StatusOK, items)
}

// GetByID GET /matches/:matchId
//
// Lo lee quien juega el partido: el plantel de cualquiera de los dos equipos, o
// el invitado citado a este. Sin esta guarda alcanzaba con tener sesión y el id
// del partido para leer el de cualquier club —cuándo, dónde y contra quién.
func (h *MatchHandler) GetByID(c *gin.Context) {
	m, ok := h.loadMatchForMember(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, m)
}

// ScheduleConflicts GET /teams/:id/matches/conflicts?at=2026-08-12T15:00:00Z
//
// Avisa, no bloquea: un manager puede tener razones para agendar dos cosas el
// mismo día y la app no debería decidir por él.
func (h *MatchHandler) ScheduleConflicts(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	at, err := time.Parse(time.RFC3339, c.Query("at"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at must be an RFC3339 datetime"})
		return
	}

	conflicts, err := h.matches.ListByTeamOnDate(c.Request.Context(), teamID, at)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not check conflicts"})
		return
	}
	if conflicts == nil {
		conflicts = []*match.Match{}
	}
	c.JSON(http.StatusOK, conflicts)
}

// ListCallups GET /matches/:matchId/callups
func (h *MatchHandler) ListCallups(c *gin.Context) {
	m, ok := h.loadMatchForMember(c)
	if !ok {
		return
	}

	callups, err := h.matches.ListCallups(c.Request.Context(), m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list callups"})
		return
	}
	if callups == nil {
		callups = []*match.Callup{}
	}
	c.JSON(http.StatusOK, callups)
}

// ListCallupsByMembership GET /memberships/:membershipId/callups
// Es el historial de asistencia de un jugador.
func (h *MatchHandler) ListCallupsByMembership(c *gin.Context) {
	membershipID := c.Param("membershipId")

	target, err := h.memberships.GetMemberByID(c.Request.Context(), membershipID)
	if errors.Is(err, membership.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "membership not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	/*
		El plantel se lee el historial entre sí; el invitado, solo el suyo.

		Antes esto era `requireMember`, que deja afuera a los invitados, y con
		eso el invitado no podía leer ni sus propias convocatorias: la app le
		mostraba un inicio sin el partido al que acababa de confirmar. Su
		vínculo con el equipo es ese partido, así que lo suyo tiene que poder
		verlo; el historial de los demás no, que es información de plantel.

		Es el mismo par que ya usa `ChargeHandler.ListByMembership`: entrar con
		`requireMembership` —que cuenta a los invitados— y acotar después.
	*/
	me, err := h.authz.requireMembership(c, target.TeamID)
	if abortAuthz(c, err) {
		return
	}
	if me.IsGuest() && me.ID != membershipID {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}

	callups, err := h.matches.ListCallupsByMembership(c.Request.Context(), membershipID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list callups"})
		return
	}
	if callups == nil {
		callups = []*match.Callup{}
	}
	c.JSON(http.StatusOK, callups)
}

// CallUp POST /matches/:matchId/callups
// Convoca jugadores. Solo el manager del equipo que juega.
func (h *MatchHandler) CallUp(c *gin.Context) {
	m, err := h.matches.FindByID(c.Request.Context(), c.Param("matchId"))
	if errors.Is(err, match.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	var req struct {
		TeamID        string   `json:"team_id"        binding:"required"`
		MembershipIDs []string `json:"membership_ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !m.Involves(req.TeamID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "that team does not play this match"})
		return
	}
	if _, err := h.authz.requireRole(c, req.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	// Cada membresía tiene que ser del equipo que convoca. Sin esta vuelta, un
	// manager podría convocar jugadores del rival mandando ids ajenos.
	members, err := h.memberships.ListByTeam(c.Request.Context(), req.TeamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not verify roster"})
		return
	}
	// Los invitados quedan afuera de la citación normal a propósito: su vínculo
	// se apaga con el partido por el que entraron. Volver a llamar a un parche
	// del mes pasado le devolvería acceso a un partido nuevo sin que él haya
	// aceptado nada, así que para eso va un enlace nuevo —que es un acto
	// explícito del manager y una decisión explícita de la persona.
	own := make(map[string]bool, len(members))
	for _, member := range members {
		if member.Status == membership.StatusActive && !member.IsGuest() {
			own[member.MembershipID] = true
		}
	}
	for _, id := range req.MembershipIDs {
		if !own[id] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "one of the players is not an active member of this team"})
			return
		}
	}

	callups, err := h.matches.CallUp(c.Request.Context(), m.ID, req.MembershipIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not call up players"})
		return
	}

	// Proyección a Firestore: es lo que hace que la citación aparezca en el
	// teléfono del resto del plantel sin que nadie recargue nada.
	userByMembership := make(map[string]string, len(members))
	for _, member := range members {
		userByMembership[member.MembershipID] = member.UserID
	}
	projected := make([]firebase.Callup, 0, len(callups))
	for _, cu := range callups {
		if userID, ok := userByMembership[cu.MembershipID]; ok {
			projected = append(projected, firebase.Callup{
				TeamID:  req.TeamID,
				MatchID: m.ID,
				UserID:  userID,
				Status:  string(cu.Status),
			})
		}
	}
	h.firebase.SyncCallupsAsync(projected)

	// Es el aviso que más se espera de la app: hasta ahora había que perseguir
	// uno por uno para saber quién va.
	h.notifications.NotifyAsync(
		userIDsForMemberships(members, req.MembershipIDs),
		"Te convocaron",
		"Estás citado para un partido. Toca para confirmar si vas.",
		map[string]string{"type": "callup_created", "match_id": m.ID},
	)

	c.JSON(http.StatusCreated, callups)
}

// RespondToCallup POST /matches/:matchId/callups/respond
// El jugador contesta por sí mismo; el membershipId sale del token, no del body.
func (h *MatchHandler) RespondToCallup(c *gin.Context) {
	m, err := h.matches.FindByID(c.Request.Context(), c.Param("matchId"))
	if errors.Is(err, match.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	var req struct {
		TeamID    string `json:"team_id" binding:"required"`
		Attending *bool  `json:"attending" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !m.Involves(req.TeamID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "that team does not play this match"})
		return
	}

	// La membresía se resuelve desde el token: un jugador responde por sí mismo
	// y por nadie más. Aceptar un membershipId del body dejaría que cualquiera
	// confirme o rechace en nombre de un compañero.
	//
	// El invitado también responde acá —puede avisar que al final no llega— y
	// por eso pasa por el guard del partido y no por el del equipo.
	me, err := h.requireMatchParticipant(c, req.TeamID, m)
	if abortAuthz(c, err) {
		return
	}

	callup, err := h.matches.Respond(c.Request.Context(), m.ID, me.ID, *req.Attending, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save your answer"})
		return
	}

	// La respuesta pasa por acá y no se escribe desde el teléfono: Postgres es
	// la autoridad y Firestore la copia. El round-trip extra cuesta
	// milisegundos y evita que las dos versiones puedan discrepar.
	h.firebase.SyncCallupsAsync([]firebase.Callup{{
		TeamID:  req.TeamID,
		MatchID: m.ID,
		UserID:  me.UserID,
		Status:  string(callup.Status),
	}})

	// Aceptar es comprometerse con la cuota de la cancha, así que el cargo nace
	// acá y no cuando el manager reparte. La cuota por jugador ya quedó fijada
	// al crear la competencia, de modo que no hay nada que esperar: el jugador
	// puede pagar en el acto o dejarlo para después, y el reparto del manager
	// pasa a ser el recordatorio de los que lo dejaron para después.
	h.syncMatchCharge(c.Request.Context(), m, req.TeamID, me.ID, *req.Attending)

	c.JSON(http.StatusOK, callup)
}

// syncMatchCharge deja el cargo del jugador acorde a lo que acaba de responder:
// existe si va, y desaparece —solo si nadie lo pagó— si dijo que no.
//
// No devuelve error a propósito: la respuesta a la citación ya está guardada y
// es lo que el jugador vino a hacer. Fallar acá y devolverle un error lo dejaría
// creyendo que no quedó anotado, que es peor que un cargo que el reparto del
// manager va a corregir igual.
func (h *MatchHandler) syncMatchCharge(
	ctx context.Context, m *match.Match, teamID, membershipID string, attending bool,
) {
	source := charge.Source{Type: charge.SourceMatchCost, ID: m.CompetitionID}

	if !attending {
		if err := h.charges.RemovePendingForMembership(ctx, source, membershipID); err != nil {
			slog.Error("could not remove the charge of a player who declined",
				"error", err, "match_id", m.ID, "membership_id", membershipID)
		}
		return
	}

	ensureMatchCharge(ctx, h.charges, h.competitions, m, teamID, membershipID)
}

// ensureMatchCharge deja creado el cargo de la cancha de quien va a jugar.
//
// Vive suelta y no como método porque hay dos puertas por las que alguien queda
// confirmado a un partido —responder la citación y canjear un enlace de
// invitado— y las dos tienen que cobrar igual. Duplicarla era dejar al parche
// jugando gratis hasta que el manager se acordara de rehacer el reparto.
func ensureMatchCharge(
	ctx context.Context,
	charges charge.Repository,
	competitions competition.Repository,
	m *match.Match,
	teamID, membershipID string,
) {
	comp, err := competitions.FindByID(ctx, m.CompetitionID)
	if err != nil {
		slog.Error("could not read the competition to charge the venue",
			"error", err, "competition_id", m.CompetitionID)
		return
	}
	// La cuota se guardó al crear la competencia: acá se lee, no se recalcula.
	// Nula significa que nadie le puso precio al lugar, que es un caso normal y
	// no un error.
	if comp.PlayerShare == nil || *comp.PlayerShare <= 0 || comp.VenueCost == nil {
		return
	}

	if _, err := charges.EnsureForMembership(ctx, charge.EnsureInput{
		TeamID:       teamID,
		MembershipID: membershipID,
		Source:       charge.Source{Type: charge.SourceMatchCost, ID: m.CompetitionID},
		Amount:       *comp.PlayerShare,
		Currency:     comp.VenueCost.Currency,
	}); err != nil {
		slog.Error("could not create the charge of a player who confirmed",
			"error", err, "match_id", m.ID, "membership_id", membershipID)
	}
}

// loadMatchForMember carga el partido y exige poder verlo: estar en el plantel
// de alguno de los dos equipos, o ser el invitado convocado a ESTE partido.
func (h *MatchHandler) loadMatchForMember(c *gin.Context) (*match.Match, bool) {
	m, err := h.matches.FindByID(c.Request.Context(), c.Param("matchId"))
	if errors.Is(err, match.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}

	for _, teamID := range []string{m.HomeTeamID, m.AwayTeamID} {
		if _, err := h.requireMatchParticipant(c, teamID, m); err == nil {
			return m, true
		}
	}

	c.JSON(http.StatusForbidden, gin.H{"error": ErrNotMember.Error()})
	return nil, false
}

/*
requireMatchParticipant resuelve quién pide, contra un partido concreto.

Del plantel entra cualquiera del equipo. El invitado entra solo si tiene
convocatoria a este partido, y ahí está toda la regla del "parche": su vínculo
es con el partido y no con el club, así que su llave es la fila de
match_callups, no la membresía.

Sin este corte, la membresía de invitado sería una membresía normal y el que
entró por un enlace de WhatsApp a jugar un sábado pasaría a ver el calendario
entero del equipo.
*/
func (h *MatchHandler) requireMatchParticipant(
	c *gin.Context, teamID string, m *match.Match,
) (*membership.Membership, error) {
	me, err := h.authz.requireMembership(c, teamID)
	if err != nil {
		return nil, err
	}
	if !me.IsGuest() {
		return me, nil
	}

	callups, err := h.matches.ListCallups(c.Request.Context(), m.ID)
	if err != nil {
		return nil, err
	}
	for _, cu := range callups {
		if cu.MembershipID == me.ID {
			return me, nil
		}
	}
	return nil, ErrGuestScope
}
