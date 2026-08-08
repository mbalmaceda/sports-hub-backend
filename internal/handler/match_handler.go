package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

type MatchHandler struct {
	matches       match.Repository
	memberships   membership.Repository
	notifications *notification.Service
	authz         teamAuthorizer
}

func NewMatchHandler(
	matches match.Repository,
	memberships membership.Repository,
	notifications *notification.Service,
) *MatchHandler {
	return &MatchHandler{
		matches:       matches,
		memberships:   memberships,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
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
func (h *MatchHandler) ListByCompetition(c *gin.Context) {
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
func (h *MatchHandler) GetByID(c *gin.Context) {
	m, err := h.matches.FindByID(c.Request.Context(), c.Param("matchId"))
	if errors.Is(err, match.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
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

	// Basta pertenecer al equipo: el historial de asistencia es información de
	// plantel, la ve cualquier compañero.
	if _, err := h.authz.requireMember(c, target.TeamID); abortAuthz(c, err) {
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
	own := make(map[string]bool, len(members))
	for _, member := range members {
		if member.Status == membership.StatusActive {
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
	me, err := h.authz.requireMember(c, req.TeamID)
	if abortAuthz(c, err) {
		return
	}

	callup, err := h.matches.Respond(c.Request.Context(), m.ID, me.ID, *req.Attending, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save your answer"})
		return
	}

	c.JSON(http.StatusOK, callup)
}

// loadMatchForMember carga el partido y exige pertenecer a alguno de los dos
// equipos que juegan.
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
		if _, err := h.authz.requireMember(c, teamID); err == nil {
			return m, true
		}
	}

	c.JSON(http.StatusForbidden, gin.H{"error": ErrNotMember.Error()})
	return nil, false
}
