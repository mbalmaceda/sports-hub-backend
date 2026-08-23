package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/onboarding"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

type OnboardingHandler struct {
	onboarding  onboarding.Repository
	teams       team.Repository
	memberships membership.Repository
	// users entra por un solo aviso: quien pide sumarse a un equipo todavía no
	// tiene membresía ahí, así que su nombre no se puede sacar del plantel.
	users         user.Repository
	notifications *notify.Service
	firebase      *firebase.Firebase
	authz         teamAuthorizer
}

func NewOnboardingHandler(
	repo onboarding.Repository,
	teams team.Repository,
	memberships membership.Repository,
	users user.Repository,
	notifications *notify.Service,
	fb *firebase.Firebase,
) *OnboardingHandler {
	return &OnboardingHandler{
		onboarding:    repo,
		teams:         teams,
		memberships:   memberships,
		users:         users,
		notifications: notifications,
		firebase:      fb,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

// userName resuelve el nombre de alguien que todavía no es del equipo. Igual que
// los otros resolutores de nombre: si no se puede leer, el aviso sale con un
// genérico antes que no salir.
func (h *OnboardingHandler) userName(ctx context.Context, userID string) string {
	const unknown = "Alguien"
	if h.users == nil {
		return unknown
	}
	u, err := h.users.FindByID(ctx, userID)
	if err != nil || u.Name == "" {
		return unknown
	}
	return u.Name
}

/*
announceJoin avisa que hay alguien nuevo en el plantel.

Las dos puertas terminan acá —la invitación que la persona aceptó y la solicitud
que el manager aprobó— porque para el resto del cuerpo técnico el hecho es el
mismo: hay uno más. `except` es lo que las diferencia: en la solicitud, el
manager que apretó "aceptar" no necesita que le avisen de lo que acaba de hacer.

Apunta a la membresía recién creada, que es la ficha del jugador. Si no se puede
leer, no se manda nada: un aviso sin destino en un flujo donde el destino es todo
el contenido —"mira quién entró"— no vale la interrupción.
*/
func (h *OnboardingHandler) announceJoin(ctx context.Context, teamID, userID string, except ...string) {
	if !h.notifications.Enabled() {
		return
	}
	m, err := h.memberships.FindByUserAndTeam(ctx, userID, teamID)
	if err != nil {
		slog.Error("could not read the new membership to announce it",
			"error", err, "team_id", teamID, "user_id", userID)
		return
	}
	h.notifications.EmitAsync(notify.Event{
		TeamID:     teamID,
		Type:       notification.TypePlayerJoined,
		EntityID:   m.ID,
		Title:      "Se sumó al equipo",
		Body:       h.userName(ctx, userID) + " ya es parte del plantel.",
		Recipients: notify.To(managerIDs(ctx, h.memberships, teamID, except...)...),
	})
}

// syncMirror refleja en Firestore la membresía que acaba de nacer al aceptar una
// invitación o una solicitud.
//
// Acá la membresía la crea el repositorio de onboarding, no el handler, así que
// hay que ir a buscarla: sin esto, quien entra por invitación no existe para las
// reglas y no puede leer nada de su equipo nuevo.
func (h *OnboardingHandler) syncMirror(ctx context.Context, teamID, userID string) {
	if !h.firebase.Enabled() {
		return
	}
	m, err := h.memberships.FindByUserAndTeam(ctx, userID, teamID)
	if err != nil {
		slog.Error("mirror: could not read new membership",
			"error", err, "team_id", teamID, "user_id", userID)
		return
	}
	h.firebase.SyncMembershipAsync(firebase.Membership{
		TeamID: m.TeamID,
		UserID: m.UserID,
		Role:   string(m.Role),
		Status: string(m.Status),
		// Va explícito porque acá puede llegar un parche recién promovido, y
		// su documento en Firestore todavía dice `kind: 'guest'` con el
		// `matchId` que lo encierra en ese partido. `SyncMembership` escribe
		// con `Set`, así que mandar el kind de Postgres es lo que lo suelta.
		Kind: string(m.Kind),
	})
}

/*
¿Esta persona ya es del club?

No es lo mismo que tener una membresía en él, y confundirlos es lo que
tenía trabado al parche: el invitado TIENE una membresía en el equipo al
que vino a jugar —es un `membership_id`, así se le cobra la cancha y se le
guarda la convocatoria— y justamente por eso `FindByUserAndTeam` lo
encontraba y las dos puertas de entrada al club le contestaban "ya sos
miembro". No lo es: es el que más cerca está de querer serlo.

La pregunta se contesta acá y no comparando el campo suelto en cada
handler, por lo mismo que `IsGuest` existe en el dominio.
*/
func (h *OnboardingHandler) belongsToTeam(ctx context.Context, userID, teamID string) bool {
	m, err := h.memberships.FindByUserAndTeam(ctx, userID, teamID)
	return err == nil && !m.IsGuest()
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
	// manager con una invitación que nunca se va a responder. Al parche sí se
	// lo puede invitar, y es el caso que más importa: el manager acaba de
	// verlo jugar, que es el mejor momento que va a haber para sumarlo.
	if h.belongsToTeam(c.Request.Context(), req.UserID, teamID) {
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

	/*
		Para quien todavía no tiene equipo, esta es la única notificación que le
		puede llegar, así que vale la pena nombrar al equipo en vez de un aviso
		genérico.

		Ojo con el destinatario: no es miembro de este equipo ni de ninguno. La
		pantalla a la que lleva tiene que cargar sin equipo activo —igual que la
		del parche— porque si cayera en la pestaña de Competencia se encontraría
		con el estado de "todavía no tienes equipo" en vez de con su invitación.
	*/
	if h.notifications.Enabled() {
		ctx := c.Request.Context()
		h.notifications.EmitAsync(notify.Event{
			TeamID:     teamID,
			Type:       notification.TypeTeamInvitation,
			EntityID:   inv.ID,
			Title:      "Te invitaron a un equipo",
			Body:       teamName(ctx, h.teams, teamID) + " te invitó a sumarte. Toca para responder.",
			Recipients: notify.To(req.UserID),
		})
	}

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

	// Solo si aceptó: rechazar no crea membresía y no hay nada que reflejar.
	if *req.Accept {
		h.syncMirror(c.Request.Context(), inv.TeamID, inv.UserID)
		h.announceJoin(c.Request.Context(), inv.TeamID, inv.UserID)
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
	// El parche puede pedir entrar al club en el que jugó: tiene membresía ahí
	// pero no pertenece. Ver `belongsToTeam`.
	if h.belongsToTeam(c.Request.Context(), userID, teamID) {
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

	// La puerta espejo de la invitación, y la que más falta hacía: hasta acá una
	// solicitud se quedaba esperando hasta que al manager se le ocurriera entrar
	// a mirar, con una persona del otro lado esperando respuesta.
	if h.notifications.Enabled() {
		ctx := c.Request.Context()
		h.notifications.EmitAsync(notify.Event{
			TeamID:     teamID,
			Type:       notification.TypeJoinRequested,
			EntityID:   request.ID,
			Title:      "Quieren sumarse al equipo",
			Body:       h.userName(ctx, userID) + " pidió entrar. Toca para responder.",
			Recipients: notify.To(managerIDs(ctx, h.memberships, teamID)...),
		})
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

	if *req.Accept {
		h.syncMirror(c.Request.Context(), request.TeamID, request.UserID)
		// El que aprobó queda afuera: acaba de hacerlo, no necesita que se lo
		// cuenten. El resto del cuerpo técnico sí.
		h.announceJoin(c.Request.Context(), request.TeamID, request.UserID, userID)
	}

	c.JSON(http.StatusOK, updated)
}
