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
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
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
	matches       match.Repository
	teams         team.Repository
	notifications *notify.Service
	authz         teamAuthorizer
	access        competitionAccess
	// Todo lo que hay que deshacer al cancelar. Ver `Cancel`.
	charges     charge.Repository
	settlements settlement.Repository
	funds       funds.Repository
	invites     guest.Repository
}

func NewCompetitionHandler(
	competitions competition.Repository,
	memberships membership.Repository,
	matches match.Repository,
	teams team.Repository,
	charges charge.Repository,
	settlements settlement.Repository,
	teamFunds funds.Repository,
	invites guest.Repository,
	notifications *notify.Service,
) *CompetitionHandler {
	authz := teamAuthorizer{memberships: memberships}
	return &CompetitionHandler{
		competitions:  competitions,
		matches:       matches,
		teams:         teams,
		notifications: notifications,
		authz:         authz,
		charges:       charges,
		settlements:   settlements,
		funds:         teamFunds,
		invites:       invites,
		// El repositorio de partidos entra solo por esto: la regla de acceso
		// necesita saber si un invitado tiene convocatoria a alguno de los
		// partidos de la competencia.
		access: competitionAccess{authz: authz, competitions: competitions, matches: matches},
	}
}

/*
Cancel POST /competitions/:competitionId/cancel

Da de baja un partido que ya movió gente y plata. Lo hace el manager del equipo
**organizador**: es el que reservó y pagó la cancha, así que es el que puede
soltarla. El rival que se cae tiene que pedírselo — dejarlo cancelar por su
cuenta sería que un tercero deshaga el compromiso que el otro ya pagó.

Es **irreversible**. No hay un "descancelar" porque volver atrás tendría que
resucitar los cobros anulados adivinando cuáles estaban pagados, y un partido
que se recupera se vuelve a acordar: es un desafío nuevo, no este.

El corte para poder cancelar es el **resultado y no la hora**: un partido se
suspende a la hora del pitazo —llueve, no se juntó la gente— así que la hora no
puede bloquear. Uno con marcador cargado ya se jugó, y ahí lo que corresponde es
corregir el marcador.

Lo que deshace, en orden, y por qué cada cosa:

 1. Los **cobros**, todos, incluidos los pagados. La plata que los jugadores
    pusieron por una cancha que no se usó no es del equipo: dejarlos en 'paid'
    la contaría como ingreso del mes. Pasan a 'cancelled', que no suma en
    ningún lado y conserva quién había pagado cuánto — la única lista de a
    quién devolverle.
 2. La **liquidación** entre equipos, por lo mismo. Si el rival ya había
    transferido su mitad, el organizador tiene plata ajena que devolver.
 3. Los **fondos** del reparto: un excedente de un partido que no se jugó es
    plata inventada. `funds.Set` con cero borra la entrada.
 4. Los **enlaces de invitados** vigentes, o alguien se suma por WhatsApp a un
    partido que ya no existe.
 5. Los **partidos** y la competencia.

Ninguno de esos pasos aborta la cancelación si falla, y es a propósito: el
partido se cancela igual. Un cobro que quedó vivo se puede volver a anular; una
competencia a medio cancelar —con la cancha soltada y la gente esperando— no
tiene arreglo desde ninguna pantalla.
*/
func (h *CompetitionHandler) Cancel(c *gin.Context) {
	ctx := c.Request.Context()
	competitionID := c.Param("competitionId")

	comp, err := h.competitions.FindByID(ctx, competitionID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	me, err := h.authz.requireRole(c, comp.OrganizerTeamID, membership.RoleManager)
	if abortAuthz(c, err) {
		return
	}

	if comp.Status == competition.StatusCancelled {
		c.JSON(http.StatusConflict, gin.H{"error": "this competition was already cancelled"})
		return
	}

	matches, err := h.matches.ListByCompetition(ctx, competitionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	for _, m := range matches {
		if m.HasResult() {
			c.JSON(http.StatusConflict, gin.H{
				"error": "this match already has a result: correct the score instead of cancelling",
			})
			return
		}
	}

	source := charge.Source{Type: charge.SourceMatchCost, ID: competitionID}

	refundable, err := h.charges.CancelBySource(ctx, source)
	if err != nil {
		slog.Error("could not cancel the charges of a cancelled match",
			"error", err, "competition_id", competitionID)
	}

	paidSettlement, err := h.settlements.Cancel(ctx, settlement.Source{
		Type: settlement.SourceMatchCost, ID: competitionID,
	})
	if err != nil {
		slog.Error("could not cancel what the rival owed for the venue",
			"error", err, "competition_id", competitionID)
	}

	// El excedente del reparto se borra para los dos equipos: cada uno guardó
	// el suyo, y el que no organizó también tiene fondo si repartió.
	for _, teamID := range teamsOf(matches) {
		if err := h.funds.Set(ctx, teamID, funds.Source{
			Type: funds.SourceMatchCost, ID: competitionID,
		}, 0, ""); err != nil {
			slog.Error("could not clear the funds of a cancelled match",
				"error", err, "competition_id", competitionID, "team_id", teamID)
		}
	}

	now := time.Now()
	for _, m := range matches {
		if err := h.matches.UpdateStatus(ctx, m.ID, match.StatusCancelled); err != nil {
			slog.Error("could not cancel the match",
				"error", err, "match_id", m.ID)
		}
		invites, err := h.invites.ListByMatch(ctx, m.ID)
		if err != nil {
			continue
		}
		for _, inv := range invites {
			if inv.RevokedAt == nil {
				_ = h.invites.Revoke(ctx, inv.ID, now)
			}
		}
	}

	if err := h.competitions.UpdateStatus(ctx, competitionID, competition.StatusCancelled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not cancel the competition"})
		return
	}

	h.notifyCancelled(ctx, matches, me.UserID, len(refundable) > 0, paidSettlement)

	c.JSON(http.StatusOK, gin.H{
		"competition_id": competitionID,
		// Cuántos cobros hay que devolver y por cuánto. La app lo usa para
		// confirmar en pantalla lo que el manager acaba de asumir.
		"refunds":      len(refundable),
		"refund_total": totalOf(refundable),
	})
}

/*
notifyCancelled avisa a los dos lados que el partido no va.

Va a **todo el que fue citado**, no solo a los managers: el que confirmó y se
guardó el sábado es el primero que tiene que enterarse, y es el aviso más urgente
de la app —a diferencia de casi todos los demás, este pierde valor con cada hora
que pasa—.

El texto cambia según haya plata de por medio, y esa diferencia importa: si nadie
pagó, la baja es solo una agenda que se libera. Si hubo cobros, la persona tiene
una devolución esperando y necesita saberlo desde el aviso, no al abrir la app
tres días después.

El destino es el **partido**, igual que el resto de los avisos de partido: su
resumen es el que queda diciendo que se canceló y, para quien maneja la plata, a
quién hay que devolverle. Se manda el primero porque hoy toda competencia
cancelable tiene uno solo —el amistoso y el interno—; el día que se cancele un
partido suelto de un torneo, esto pasa a ser por partido y no por competencia.
*/
func (h *CompetitionHandler) notifyCancelled(
	ctx context.Context,
	matches []*match.Match,
	actor string,
	hasRefunds bool,
	paidSettlement *settlement.Settlement,
) {
	if !h.notifications.Enabled() {
		return
	}

	body := "El partido no se juega."
	if hasRefunds {
		body = "El partido no se juega. Tu manager te devuelve lo que pagaste."
	}

	// Sin partido no hay a dónde llevar. No debería pasar —una competencia
	// activa siempre tiene el suyo— pero mandar un aviso que abre en la nada es
	// peor que no mandarlo.
	if len(matches) == 0 {
		return
	}
	entityID := matches[0].ID

	for _, teamID := range teamsOf(matches) {
		h.notifications.EmitAsync(notify.Event{
			TeamID:   teamID,
			Type:     notification.TypeMatchCancelled,
			EntityID: entityID,
			Title:    "Se canceló el partido",
			Body:     body,
			Recipients: notify.To(teamUserIDs(ctx, h.authz.memberships, teamID, func(m *membership.TeamMember) bool {
				return m.UserID != actor
			})...),
		})
	}

	// La mitad que el rival ya había transferido es una devolución entre
	// managers, y el que la espera es el del equipo retado. Va aparte porque el
	// aviso de arriba habla de la cuota propia de cada jugador, que es otra
	// plata y otro interlocutor.
	if paidSettlement != nil && paidSettlement.Status == settlement.StatusPaid {
		h.notifications.EmitAsync(notify.Event{
			TeamID:   paidSettlement.FromTeamID,
			Type:     notification.TypeMatchCancelled,
			EntityID: entityID,
			Title:    "Se canceló el partido",
			Body:     "Ya habías transferido tu mitad de la cancha: el otro equipo tiene que devolvértela.",
			Recipients: notify.To(
				managerIDs(ctx, h.authz.memberships, paidSettlement.FromTeamID, actor)...,
			),
		})
	}
}

// teamsOf junta los equipos que jugaban, sin repetir. En un partido interno los
// dos lados son el mismo, y borrarle el fondo dos veces no rompe pero sobra.
func teamsOf(matches []*match.Match) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		for _, id := range []string{m.HomeTeamID, m.AwayTeamID} {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func totalOf(charges []*charge.Charge) int64 {
	var total int64
	for _, ch := range charges {
		total += ch.Amount
	}
	return total
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
