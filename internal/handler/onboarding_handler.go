package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/onboarding"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

type OnboardingHandler struct {
	onboarding    onboarding.Repository
	teams         team.Repository
	memberships   membership.Repository
	notifications *notification.Service
	authz         teamAuthorizer
}

func NewOnboardingHandler(
	repo onboarding.Repository,
	teams team.Repository,
	memberships membership.Repository,
	notifications *notification.Service,
) *OnboardingHandler {
	return &OnboardingHandler{
		onboarding:    repo,
		teams:         teams,
		memberships:   memberships,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

// FindPerson GET /people/lookup?method=tax_id&value=12.345.678-9
//
// Coincidencia exacta y nada más. Una búsqueda difusa por nombre convertiría el
// padrón de usuarios en un directorio navegable por cualquier manager, y acá
// hay datos personales de por medio.
func (h *OnboardingHandler) FindPerson(c *gin.Context) {
	method := onboarding.LookupMethod(c.Query("method"))
	if method != onboarding.LookupByTaxID && method != onboarding.LookupByEmail {
		c.JSON(http.StatusBadRequest, gin.H{"error": "method must be tax_id or email"})
		return
	}

	value := strings.TrimSpace(c.Query("value"))
	if value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value is required"})
		return
	}

	person, err := h.onboarding.FindPerson(c.Request.Context(), method, value)
	if errors.Is(err, onboarding.ErrPersonNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no registered person matches that data"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, person)
}

// SearchTeams GET /teams/search?q=riverside
func (h *OnboardingHandler) SearchTeams(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	// Dos caracteres como mínimo: con uno solo devolvería medio padrón de
	// equipos y no ayudaría a encontrar nada.
	if len(query) < 2 {
		c.JSON(http.StatusOK, []*team.Team{})
		return
	}

	teams, err := h.teams.SearchByName(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not search teams"})
		return
	}
	if teams == nil {
		teams = []*team.Team{}
	}
	c.JSON(http.StatusOK, teams)
}

// ─── Invitaciones del equipo hacia una persona ──────────────────────────────

// ListTeamInvitations GET /teams/:id/invitations
func (h *OnboardingHandler) ListTeamInvitations(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	invitations, err := h.onboarding.ListInvitationsForTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list invitations"})
		return
	}
	if invitations == nil {
		invitations = []*onboarding.TeamInvitation{}
	}
	c.JSON(http.StatusOK, invitations)
}

// ListMyInvitations GET /me/team-invitations
func (h *OnboardingHandler) ListMyInvitations(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	invitations, err := h.onboarding.ListInvitationsForUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list invitations"})
		return
	}
	if invitations == nil {
		invitations = []*onboarding.TeamInvitation{}
	}
	c.JSON(http.StatusOK, invitations)
}

// InvitePerson POST /teams/:id/invitations
func (h *OnboardingHandler) InvitePerson(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}
	userID, _ := currentUserID(c)

	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Invitar a alguien que ya está adentro no tiene sentido y confundiría al
	// manager con una invitación que nunca se va a responder.
	if _, err := h.memberships.FindByUserAndTeam(c.Request.Context(), req.UserID, teamID); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": onboarding.ErrAlreadyMember.Error()})
		return
	}

	inv := &onboarding.TeamInvitation{
		TeamID:          teamID,
		InvitedByUserID: userID,
		UserID:          req.UserID,
		Status:          onboarding.InvitationSent,
	}
	if err := h.onboarding.CreateInvitation(c.Request.Context(), inv); err != nil {
		// El índice parcial rechaza una segunda invitación abierta a la misma
		// persona: es condición esperable, no una falla del servidor.
		c.JSON(http.StatusConflict, gin.H{"error": "there is already an open invitation for this person"})
		return
	}

	// Para quien todavía no tiene equipo, esta es la única notificación que le
	// puede llegar, así que vale la pena nombrar al equipo en vez de un aviso
	// genérico. Si no se puede leer el nombre se manda igual: el aviso importa
	// más que el detalle.
	body := "Un equipo te invitó a sumarte. Toca para responder."
	if t, err := h.teams.FindByID(c.Request.Context(), teamID); err == nil {
		body = t.Name + " te invitó a sumarte. Toca para responder."
	}
	h.notifications.NotifyAsync(
		[]string{req.UserID},
		"Te invitaron a un equipo",
		body,
		map[string]string{"type": "team_invitation", "invitation_id": inv.ID},
	)

	c.JSON(http.StatusCreated, inv)
}

// RespondToInvitation POST /team-invitations/:invitationId/respond
// La responde la persona invitada, no el equipo que invitó.
func (h *OnboardingHandler) RespondToInvitation(c *gin.Context) {
	inv, err := h.onboarding.FindInvitation(c.Request.Context(), c.Param("invitationId"))
	if errors.Is(err, onboarding.ErrInvitationNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if inv.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "this invitation is addressed to someone else"})
		return
	}

	var req struct {
		Accept *bool `json:"accept" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.onboarding.RespondToInvitation(c.Request.Context(), inv.ID, *req.Accept, time.Now())
	if errors.Is(err, onboarding.ErrAlreadyAnswered) {
		c.JSON(http.StatusConflict, gin.H{"error": onboarding.ErrAlreadyAnswered.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not respond to the invitation"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// ─── Solicitudes de la persona hacia el equipo ──────────────────────────────

// ListJoinRequests GET /teams/:id/join-requests
func (h *OnboardingHandler) ListJoinRequests(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	requests, err := h.onboarding.ListJoinRequestsForTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list join requests"})
		return
	}
	if requests == nil {
		requests = []*onboarding.JoinRequest{}
	}
	c.JSON(http.StatusOK, requests)
}

// RequestToJoin POST /teams/:id/join-requests
// La manda cualquiera que esté autenticado: es justamente el camino de quien
// todavía no pertenece al equipo.
func (h *OnboardingHandler) RequestToJoin(c *gin.Context) {
	teamID := c.Param("id")
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if _, err := h.teams.FindByID(c.Request.Context(), teamID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if _, err := h.memberships.FindByUserAndTeam(c.Request.Context(), userID, teamID); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": onboarding.ErrAlreadyMember.Error()})
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	_ = c.ShouldBindJSON(&req)

	request := &onboarding.JoinRequest{
		TeamID:  teamID,
		UserID:  userID,
		Message: req.Message,
		Status:  onboarding.JoinPending,
	}
	if err := h.onboarding.CreateJoinRequest(c.Request.Context(), request); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "you already have a pending request for this team"})
		return
	}
	c.JSON(http.StatusCreated, request)
}

// RespondToJoinRequest POST /join-requests/:requestId/respond
// La responde el manager del equipo; aceptar da de alta la membresía.
func (h *OnboardingHandler) RespondToJoinRequest(c *gin.Context) {
	request, err := h.onboarding.FindJoinRequest(c.Request.Context(), c.Param("requestId"))
	if errors.Is(err, onboarding.ErrJoinRequestNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "join request not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireRole(c, request.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		Accept *bool `json:"accept" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := currentUserID(c)
	updated, err := h.onboarding.RespondToJoinRequest(
		c.Request.Context(), request.ID, *req.Accept, userID, time.Now(),
	)
	if errors.Is(err, onboarding.ErrAlreadyAnswered) {
		c.JSON(http.StatusConflict, gin.H{"error": onboarding.ErrAlreadyAnswered.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not respond to the request"})
		return
	}
	c.JSON(http.StatusOK, updated)
}
