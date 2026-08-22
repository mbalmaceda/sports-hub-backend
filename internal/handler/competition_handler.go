package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

// invitationTTL es cuánto vive una invitación a competencia sin respuesta.
// Vencen solas para que la bandeja no se llene de cosas de hace meses.
//
// Es un techo, no un plazo fijo: si el torneo arranca antes, vence con el
// torneo (ver `responseDeadline`).
const invitationTTL = 7 * 24 * time.Hour

type CompetitionHandler struct {
	competitions  competition.Repository
	teams         team.Repository
	notifications *notify.Service
	authz         teamAuthorizer
	access        competitionAccess
}

func NewCompetitionHandler(
	competitions competition.Repository,
	memberships membership.Repository,
	matches match.Repository,
	teams team.Repository,
	notifications *notify.Service,
) *CompetitionHandler {
	authz := teamAuthorizer{memberships: memberships}
	return &CompetitionHandler{
		competitions:  competitions,
		teams:         teams,
		notifications: notifications,
		authz:         authz,
		// El repositorio de partidos entra solo por esto: la regla de acceso
		// necesita saber si un invitado tiene convocatoria a alguno de los
		// partidos de la competencia.
		access: competitionAccess{authz: authz, competitions: competitions, matches: matches},
	}
}

// ListByTeam GET /teams/:id/competitions
func (h *CompetitionHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	items, err := h.competitions.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list competitions"})
		return
	}
	if items == nil {
		items = []*competition.Competition{}
	}
	c.JSON(http.StatusOK, items)
}

// GetByID GET /competitions/:competitionId
//
// La lee quien la juega. Ver `competitionAccess`: antes no validaba nada y
// alcanzaba con el UUID para leer la de cualquier club.
func (h *CompetitionHandler) GetByID(c *gin.Context) {
	comp, ok := h.access.requireByID(c, c.Param("competitionId"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, comp)
}

// Create POST /teams/:id/competitions
// Solo el manager organiza competencias en nombre del equipo.
func (h *CompetitionHandler) Create(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		SportID        string     `json:"sport_id" binding:"required"`
		Type           string     `json:"type"     binding:"required,oneof=friendly tournament league"`
		Name           string     `json:"name"     binding:"required"`
		StartAt        *time.Time `json:"start_at"`
		EndAt          *time.Time `json:"end_at"`
		Venue          string     `json:"venue"`
		PlayersPerSide *int       `json:"players_per_side"`
		VenueCost      *struct {
			Amount   int64  `json:"amount"   binding:"min=0"`
			Currency string `json:"currency" binding:"required"`
		} `json:"venue_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	comp := &competition.Competition{
		SportID:         req.SportID,
		Type:            competition.Type(req.Type),
		Name:            req.Name,
		OrganizerTeamID: teamID,
		Status:          competition.StatusDraft,
		StartAt:         req.StartAt,
		EndAt:           req.EndAt,
		Venue:           req.Venue,
		PlayersPerSide:  req.PlayersPerSide,
	}
	if req.VenueCost != nil {
		comp.VenueCost = &competition.VenueCost{
			Amount:   req.VenueCost.Amount,
			Currency: req.VenueCost.Currency,
		}
	}

	if err := h.competitions.Create(c.Request.Context(), comp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create competition"})
		return
	}

	// El organizador queda inscrito de una: organizar sin participar no es un
	// caso que exista todavía, y si no la app lo mostraría fuera de su propia
	// competencia.
	entry := &competition.Entry{
		CompetitionID: comp.ID,
		TeamID:        teamID,
		Status:        competition.EntryActive,
	}
	now := time.Now()
	entry.JoinedAt = &now
	if err := h.competitions.UpsertEntry(c.Request.Context(), entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "competition created but could not add organizer entry"})
		return
	}

	c.JSON(http.StatusCreated, comp)
}

// ListEntries GET /competitions/:competitionId/entries
func (h *CompetitionHandler) ListEntries(c *gin.Context) {
	if _, ok := h.access.requireByID(c, c.Param("competitionId")); !ok {
		return
	}

	entries, err := h.competitions.ListEntries(c.Request.Context(), c.Param("competitionId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list entries"})
		return
	}
	if entries == nil {
		entries = []*competition.Entry{}
	}
	c.JSON(http.StatusOK, entries)
}

// Invite POST /competitions/:competitionId/invitations
// Invita a otro equipo. Solo el manager del organizador puede hacerlo.
func (h *CompetitionHandler) Invite(c *gin.Context) {
	comp, err := h.competitions.FindByID(c.Request.Context(), c.Param("competitionId"))
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireRole(c, comp.OrganizerTeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		ToTeamID string `json:"to_team_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ToTeamID == comp.OrganizerTeamID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot invite the organizing team"})
		return
	}

	// Nunca después del arranque del torneo: una invitación que sigue "viva"
	// cuando la competencia ya empezó es una que no se puede aceptar.
	inv := &competition.Invitation{
		CompetitionID: comp.ID,
		FromTeamID:    comp.OrganizerTeamID,
		ToTeamID:      req.ToTeamID,
		Status:        competition.InvitationSent,
		ExpiresAt:     responseDeadline(time.Now(), invitationTTL, comp.StartAt),
	}
	if err := h.competitions.CreateInvitation(c.Request.Context(), inv); err != nil {
		// El índice parcial de la tabla rechaza una segunda invitación abierta
		// al mismo equipo; se responde 409 en vez de 500 porque es una condición
		// esperable, no una falla.
		c.JSON(http.StatusConflict, gin.H{"error": "there is already an open invitation for this team"})
		return
	}

	// El nombre de la competencia es lo que hace decidible el aviso: "te
	// invitaron a la Liga de Verano" se contesta sin abrir nada, "te invitaron a
	// una competencia" obliga a entrar a averiguar a cuál.
	ctx := c.Request.Context()
	if h.notifications.Enabled() {
		h.notifications.EmitAsync(notify.Event{
			TeamID:   req.ToTeamID,
			Type:     notification.TypeTournamentInvited,
			EntityID: comp.ID,
			Title:    "Te invitaron a una competencia",
			Body: teamName(ctx, h.teams, comp.OrganizerTeamID) +
				" los invitó a " + comp.Name + ". Toca para responder.",
			Recipients: notify.To(managerIDs(ctx, h.authz.memberships, req.ToTeamID)...),
		})
	}

	c.JSON(http.StatusCreated, inv)
}

// ListInvitations GET /teams/:id/competition-invitations
func (h *CompetitionHandler) ListInvitations(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	/*
		Acá corría la barrida de invitaciones vencidas, una actualización de
		tabla completa por cada listado. Ahora la hace el trabajo periódico
		(`internal/jobs`), que además la corre aunque nadie abra la app: antes,
		si el equipo no entraba, en Postgres la invitación seguía 'sent' para
		siempre y cualquier consulta que no pasara por acá veía estado viejo.

		Lo que sostiene sacarla de la lectura es que el vencimiento no depende de
		esta escritura: el móvil lo deriva con `isInvitationExpired` y responder
		una vencida devuelve 409 igual, mire lo que mire la columna.
	*/
	invitations, err := h.competitions.ListInvitationsForTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list invitations"})
		return
	}
	if invitations == nil {
		invitations = []*competition.Invitation{}
	}
	c.JSON(http.StatusOK, invitations)
}

// RespondToInvitation POST /competition-invitations/:invitationId/respond
// Responde el equipo invitado, y solo su manager.
func (h *CompetitionHandler) RespondToInvitation(c *gin.Context) {
	inv, err := h.competitions.FindInvitation(c.Request.Context(), c.Param("invitationId"))
	if errors.Is(err, competition.ErrInvitationNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// La autorización va contra el equipo DESTINO: quien invita no puede
	// aceptar por el invitado.
	if _, err := h.authz.requireRole(c, inv.ToTeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		Accept *bool `json:"accept" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if time.Now().After(inv.ExpiresAt) {
		c.JSON(http.StatusConflict, gin.H{"error": "invitation has expired"})
		return
	}

	updated, err := h.competitions.RespondToInvitation(c.Request.Context(), inv.ID, *req.Accept, time.Now())
	if errors.Is(err, competition.ErrInvitationClosed) {
		c.JSON(http.StatusConflict, gin.H{"error": "invitation was already answered"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not respond to invitation"})
		return
	}

	// Al organizador, que es el que está esperando para armar el fixture. El
	// texto dice la respuesta completa: si solo dijera "respondieron", el
	// manager tendría que entrar para saber si tiene un equipo más o uno menos.
	if h.notifications.Enabled() {
		ctx := c.Request.Context()
		actor, _ := currentUserID(c)
		answer := "no va a participar en "
		if *req.Accept {
			answer = "se suma a "
		}
		h.notifications.EmitAsync(notify.Event{
			TeamID:   inv.FromTeamID,
			Type:     notification.TypeTournamentAnswered,
			EntityID: inv.CompetitionID,
			Title:    "Respondieron tu invitación",
			Body: teamName(ctx, h.teams, inv.ToTeamID) + " " + answer +
				competitionName(ctx, h.competitions, inv.CompetitionID) + ".",
			Recipients: notify.To(managerIDs(ctx, h.authz.memberships, inv.FromTeamID, actor)...),
		})
	}

	c.JSON(http.StatusOK, updated)
}

// competitionName resuelve el nombre para el texto del aviso. Igual que
// `teamName`: si no se puede leer, el aviso sale con un genérico antes que no
// salir.
func competitionName(ctx context.Context, competitions competition.Repository, id string) string {
	comp, err := competitions.FindByID(ctx, id)
	if err != nil || comp.Name == "" {
		return "la competencia"
	}
	return comp.Name
}
