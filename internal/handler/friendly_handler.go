package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

// challengeTTL es cuánto tiene el rival para responder antes de que la
// propuesta caduque. Dos días: suficiente para consultarlo con el equipo, poco
// como para no dejar la fecha bloqueada indefinidamente.
//
// Es un techo, no un plazo fijo: si el partido es antes, vence con el partido
// (ver `responseDeadline`).
const challengeTTL = 48 * time.Hour

type FriendlyHandler struct {
	friendlies   friendly.Repository
	competitions competition.Repository
	matches      match.Repository
	settlements  settlement.Repository
	// teams entra solo por las notificaciones: un aviso que dice el nombre del
	// rival vale mucho más que "un equipo te desafió". Si no se puede leer, el
	// aviso sale igual con el genérico.
	teams         team.Repository
	notifications *notify.Service
	authz         teamAuthorizer
}

func NewFriendlyHandler(
	friendlies friendly.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
	settlements settlement.Repository,
	teams team.Repository,
	notifications *notify.Service,
) *FriendlyHandler {
	return &FriendlyHandler{
		friendlies:    friendlies,
		competitions:  competitions,
		matches:       matches,
		settlements:   settlements,
		teams:         teams,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

/*
otherSide devuelve el equipo del otro lado del desafío.

Existe porque en un amistoso no hay un "receptor" fijo: el desafío va y viene
—retador propone, retado contraoferta, retador acepta— y quien recibe cada aviso
es siempre el que no hizo la acción. Comparar contra `ChallengerTeamID` suelto
por los handlers manda la notificación de vuelta a quien acaba de tocar el botón
la mitad de las veces.
*/
func otherSide(ch *friendly.Challenge, actingTeamID string) string {
	if ch.ChallengerTeamID == actingTeamID {
		return ch.ChallengedTeamID
	}
	return ch.ChallengerTeamID
}

// ListByTeam GET /teams/:id/friendlies
func (h *FriendlyHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	items, err := h.friendlies.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list friendlies"})
		return
	}
	if items == nil {
		items = []*friendly.Challenge{}
	}
	c.JSON(http.StatusOK, items)
}

// GetByID GET /friendlies/:challengeId
func (h *FriendlyHandler) GetByID(c *gin.Context) {
	ch, err := h.friendlies.FindByID(c.Request.Context(), c.Param("challengeId"))
	if errors.Is(err, friendly.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "friendly not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// ListProposals GET /friendlies/:challengeId/proposals
func (h *FriendlyHandler) ListProposals(c *gin.Context) {
	proposals, err := h.friendlies.ListProposals(c.Request.Context(), c.Param("challengeId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list proposals"})
		return
	}
	if proposals == nil {
		proposals = []*friendly.Proposal{}
	}
	c.JSON(http.StatusOK, proposals)
}

// Create POST /teams/:id/friendlies
// Crea la competencia del amistoso y el desafío con su primera propuesta.
func (h *FriendlyHandler) Create(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		ChallengedTeamID string    `json:"challenged_team_id" binding:"required"`
		Name             string    `json:"name"               binding:"required"`
		SportID          string    `json:"sport_id"           binding:"required"`
		ProposedStartAt  time.Time `json:"proposed_start_at"  binding:"required"`
		ProposedVenue    string    `json:"proposed_venue"`
		Message          string    `json:"message"`
		PlayersPerSide   *int      `json:"players_per_side"`
		VenueCost        *struct {
			Amount   int64  `json:"amount"   binding:"min=0"`
			Currency string `json:"currency" binding:"required"`
		} `json:"venue_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ChallengedTeamID == teamID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot challenge your own team"})
		return
	}
	if req.ProposedStartAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "proposed date must be in the future"})
		return
	}

	comp := &competition.Competition{
		SportID:         req.SportID,
		Type:            competition.TypeFriendly,
		Name:            req.Name,
		OrganizerTeamID: teamID,
		Status:          competition.StatusDraft,
		StartAt:         &req.ProposedStartAt,
		Venue:           req.ProposedVenue,
		PlayersPerSide:  req.PlayersPerSide,
	}
	if req.VenueCost != nil {
		comp.VenueCost = &competition.VenueCost{Amount: req.VenueCost.Amount, Currency: req.VenueCost.Currency}
	}
	if err := h.competitions.Create(c.Request.Context(), comp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create competition"})
		return
	}

	ch := &friendly.Challenge{
		CompetitionID:    comp.ID,
		ChallengerTeamID: teamID,
		ChallengedTeamID: req.ChallengedTeamID,
		Status:           friendly.StatusPending,
		ExpiresAt:        responseDeadline(time.Now(), challengeTTL, &req.ProposedStartAt),
	}
	first := &friendly.Proposal{
		ProposedByTeamID: teamID,
		ProposedStartAt:  req.ProposedStartAt,
		ProposedVenue:    req.ProposedVenue,
		Message:          req.Message,
	}
	if err := h.friendlies.Create(c.Request.Context(), ch, first); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create friendly"})
		return
	}

	h.announceChallenge(c.Request.Context(), ch, teamID, req.ChallengedTeamID)

	c.JSON(http.StatusCreated, gin.H{"competition": comp, "challenge": ch, "proposal": first})
}

/*
CreateInternal POST /teams/:id/internal-matches

Partido interno: el equipo pone la gente de los dos lados.

No hay rival a quien desafiar, así que no hay desafío, ni propuesta, ni espera:
la competencia y el partido nacen juntos y confirmados. Toda la máquina de
negociar existe para ponerse de acuerdo con otro equipo, y acá el único que
tiene que estar de acuerdo es el que organiza.

Es el caso más común en equipos grandes —catorce personas y una cancha— y hasta
ahora la app no lo sabía representar: obligaba a inventar un rival.

El partido queda con el mismo equipo de los dos lados. No es un truco para
esquivar el modelo: en un partido interno los dos lados son de verdad el mismo
equipo, y así todo lo que ya existe —convocatorias, cobros, balance— sigue
funcionando sin enterarse de nada. Lo único que cambia es cuánta gente hay que
convocar, y eso lo decide el cliente con `players_per_side` y la bandera.
*/
func (h *FriendlyHandler) CreateInternal(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	var req struct {
		Name           string    `json:"name"     binding:"required"`
		SportID        string    `json:"sport_id" binding:"required"`
		StartAt        time.Time `json:"start_at" binding:"required"`
		Venue          string    `json:"venue"`
		PlayersPerSide *int      `json:"players_per_side"`
		VenueCost      *struct {
			Amount   int64  `json:"amount"   binding:"min=0"`
			Currency string `json:"currency" binding:"required"`
		} `json:"venue_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "match date must be in the future"})
		return
	}

	comp := &competition.Competition{
		SportID:         req.SportID,
		Type:            competition.TypeFriendly,
		Name:            req.Name,
		OrganizerTeamID: teamID,
		// Activa y no borrador: un amistoso normal arranca en borrador porque le
		// falta que el rival diga que sí. Acá no falta nadie.
		Status:         competition.StatusActive,
		StartAt:        &req.StartAt,
		Venue:          req.Venue,
		PlayersPerSide: req.PlayersPerSide,
		IsInternal:     true,
	}
	if req.VenueCost != nil {
		comp.VenueCost = &competition.VenueCost{Amount: req.VenueCost.Amount, Currency: req.VenueCost.Currency}
	}
	if err := h.competitions.Create(c.Request.Context(), comp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create competition"})
		return
	}

	now := time.Now()
	entry := &competition.Entry{
		CompetitionID: comp.ID,
		TeamID:        teamID,
		Status:        competition.EntryActive,
		JoinedAt:      &now,
	}
	if err := h.competitions.UpsertEntry(c.Request.Context(), entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not register the team in the match"})
		return
	}

	m := &match.Match{
		CompetitionID: comp.ID,
		HomeTeamID:    teamID,
		AwayTeamID:    teamID,
		ScheduledAt:   req.StartAt,
		Venue:         req.Venue,
		Status:        match.StatusConfirmed,
	}
	if err := h.matches.Create(c.Request.Context(), m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "competition created but match could not be created"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"competition": comp, "match": m})
}

// Counter POST /friendlies/:challengeId/counter
// Contraoferta: propone otra fecha o lugar y devuelve la pelota al rival.
func (h *FriendlyHandler) Counter(c *gin.Context) {
	ch, teamID, ok := h.loadForResponse(c)
	if !ok {
		return
	}

	var req struct {
		ProposedStartAt time.Time `json:"proposed_start_at" binding:"required"`
		ProposedVenue   string    `json:"proposed_venue"`
		Message         string    `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ProposedStartAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "proposed date must be in the future"})
		return
	}

	p := &friendly.Proposal{
		ChallengeID:      ch.ID,
		ProposedByTeamID: teamID,
		ProposedStartAt:  req.ProposedStartAt,
		ProposedVenue:    req.ProposedVenue,
		Message:          req.Message,
	}
	if err := h.friendlies.AddProposal(c.Request.Context(), p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not send counter-proposal"})
		return
	}

	ctx := c.Request.Context()
	actor, _ := currentUserID(c)
	target := otherSide(ch, teamID)
	h.notifications.EmitAsync(notify.Event{
		TeamID:     target,
		Type:       notification.TypeFriendlyCountered,
		EntityID:   ch.ID,
		Title:      "Te propusieron otra fecha",
		Body:       teamName(ctx, h.teams, teamID) + " contraofertó el amistoso. Toca para revisarla.",
		Recipients: notify.To(managerIDs(ctx, h.authz.memberships, target, actor)...),
	})

	c.JSON(http.StatusCreated, p)
}

// Accept POST /friendlies/:challengeId/accept
// Cierra la negociación y crea el partido confirmado con la última propuesta.
func (h *FriendlyHandler) Accept(c *gin.Context) {
	ch, actingTeamID, ok := h.loadForResponse(c)
	if !ok {
		return
	}

	latest, err := h.friendlies.LatestProposal(c.Request.Context(), ch.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the latest proposal"})
		return
	}

	/*
		La hora del partido es el plazo real, más allá de lo que diga expires_at.

		Desde `responseDeadline` un desafío no puede vencer después del partido,
		pero los que ya estaban en la base nacieron con 48 horas fijas y pueden
		seguir "abiertos" con la fecha pasada. Aceptar uno de esos creaba un
		partido con fecha anterior a hoy: nace en el historial, con convocatorias
		que nadie va a responder y cobros por una cancha que no se usó.
	*/
	if !latest.ProposedStartAt.After(time.Now()) {
		c.JSON(http.StatusConflict, gin.H{"error": friendly.ErrExpired.Error()})
		return
	}

	if err := h.friendlies.UpdateStatus(c.Request.Context(), ch.ID, friendly.StatusAccepted); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not accept friendly"})
		return
	}

	m := &match.Match{
		CompetitionID: ch.CompetitionID,
		HomeTeamID:    ch.ChallengerTeamID,
		AwayTeamID:    ch.ChallengedTeamID,
		ScheduledAt:   latest.ProposedStartAt,
		Venue:         latest.ProposedVenue,
		Status:        match.StatusConfirmed,
	}
	if err := h.matches.Create(c.Request.Context(), m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "friendly accepted but match could not be created"})
		return
	}

	// La competencia se creó con la primera propuesta; si hubo contraoferta, esa
	// fecha ya no es la que se juega. Se alinea con la del partido recién creado
	// porque es la que el móvil lee para decidir si la competencia sigue activa
	// o ya pasó al historial.
	if err := h.competitions.UpdateSchedule(
		c.Request.Context(), ch.CompetitionID, latest.ProposedStartAt, latest.ProposedVenue,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "friendly accepted but the schedule could not be saved"})
		return
	}

	// Los dos equipos quedan activos: en un amistoso ambos son participantes,
	// no organizador e invitado como en un torneo.
	now := time.Now()
	for _, teamID := range []string{ch.ChallengerTeamID, ch.ChallengedTeamID} {
		entry := &competition.Entry{
			CompetitionID: ch.CompetitionID,
			TeamID:        teamID,
			Status:        competition.EntryActive,
			JoinedAt:      &now,
		}
		if err := h.competitions.UpsertEntry(c.Request.Context(), entry); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "friendly accepted but entries could not be updated"})
			return
		}
	}

	_ = h.competitions.UpdateStatus(c.Request.Context(), ch.CompetitionID, competition.StatusActive)

	/*
		La mitad de la cancha que le toca al rival.

		La reserva y la paga el organizador —el que desafió—, así que el retado
		le debe su mitad. Nace acá y no al crear la competencia porque recién
		ahora existe el compromiso: hasta el "acepto" el desafío podía quedar
		sin respuesta, y una deuda contra un partido que nunca se jugó es basura
		en el inicio del que la recibe.

		Ojo con el supuesto: el acreedor es siempre el retador, aunque una
		contraoferta haya mudado el partido a la cancha del otro. Es la regla
		del producto —organiza el que desafía, y el que organiza paga el lugar—
		y el día que eso no alcance, lo que corresponde es que la propuesta diga
		quién pone la cancha, no adivinarlo acá.
	*/
	if comp, err := h.competitions.FindByID(c.Request.Context(), ch.CompetitionID); err == nil {
		ensureVenueSettlement(
			c.Request.Context(), h.settlements, comp, ch.ChallengerTeamID, ch.ChallengedTeamID,
		)
	} else {
		slog.Error("could not read the competition to record what the rival owes",
			"error", err, "competition_id", ch.CompetitionID)
	}

	/*
		El aviso apunta al PARTIDO y no al desafío, que es lo distinto de este.

		Aceptar es el evento más consecuente de la app: fija la fecha, activa las
		dos inscripciones y hace nacer la deuda de la mitad de la cancha. Lo que
		el otro manager tiene que hacer a continuación es convocar, y eso se hace
		en el partido; mandarlo al desafío ya cerrado sería dejarlo a un toque de
		distancia de lo único que le queda por hacer.
	*/
	actor, _ := currentUserID(c)
	h.announceResponse(c.Request.Context(), responseAnnouncement{
		challenge:  ch,
		actingTeam: actingTeamID,
		actingUser: actor,
		kind:       notification.TypeFriendlyAccepted,
		entityID:   m.ID,
		title:      "Aceptaron el amistoso",
		suffix:     " confirmó el partido. Ya puedes armar la convocatoria.",
	})

	ch.Status = friendly.StatusAccepted
	c.JSON(http.StatusOK, gin.H{"challenge": ch, "match": m})
}

// Decline POST /friendlies/:challengeId/decline
func (h *FriendlyHandler) Decline(c *gin.Context) {
	ch, actingTeamID, ok := h.loadForResponse(c)
	if !ok {
		return
	}

	if err := h.friendlies.UpdateStatus(c.Request.Context(), ch.ID, friendly.StatusDeclined); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not decline friendly"})
		return
	}
	_ = h.competitions.UpdateStatus(c.Request.Context(), ch.CompetitionID, competition.StatusCancelled)

	/*
		Rechazar cancela la competencia, y con eso la tarjeta desaparece de
		Activas del otro lado. Esta fila es el único rastro de por qué se esfumó:
		sin ella, el manager que desafió ve que su partido ya no está y no tiene
		dónde enterarse de que lo rechazaron.
	*/
	actor, _ := currentUserID(c)
	h.announceResponse(c.Request.Context(), responseAnnouncement{
		challenge:  ch,
		actingTeam: actingTeamID,
		actingUser: actor,
		kind:       notification.TypeFriendlyDeclined,
		entityID:   ch.ID,
		title:      "Rechazaron el amistoso",
		suffix:     " no va a poder esta vez.",
	})

	ch.Status = friendly.StatusDeclined
	c.JSON(http.StatusOK, ch)
}

// loadForResponse concentra los chequeos que comparten aceptar, rechazar y
// contraofertar: que el desafío exista, siga abierto, no haya expirado, que
// quien responde sea manager de uno de los dos equipos, y —lo más importante—
// que le toque. Sin lo último, un equipo aceptaría su propia propuesta y
// cerraría un partido que el rival nunca confirmó.
func (h *FriendlyHandler) loadForResponse(c *gin.Context) (*friendly.Challenge, string, bool) {
	ch, err := h.friendlies.FindByID(c.Request.Context(), c.Param("challengeId"))
	if errors.Is(err, friendly.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "friendly not found"})
		return nil, "", false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, "", false
	}

	if !ch.Status.IsOpen() {
		c.JSON(http.StatusConflict, gin.H{"error": friendly.ErrClosed.Error()})
		return nil, "", false
	}
	if time.Now().After(ch.ExpiresAt) {
		c.JSON(http.StatusConflict, gin.H{"error": friendly.ErrExpired.Error()})
		return nil, "", false
	}

	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, "", false
	}

	// Se prueba contra los dos equipos porque cualquiera de ellos puede estar
	// del lado que responde, según quién hizo la última propuesta.
	var actingTeamID string
	for _, candidate := range []string{ch.ChallengerTeamID, ch.ChallengedTeamID} {
		if m, err := h.authz.requireRole(c, candidate, membership.RoleManager); err == nil && m.UserID == userID {
			actingTeamID = candidate
			break
		}
	}
	if actingTeamID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return nil, "", false
	}

	latest, err := h.friendlies.LatestProposal(c.Request.Context(), ch.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the latest proposal"})
		return nil, "", false
	}
	if latest.ProposedByTeamID == actingTeamID {
		c.JSON(http.StatusConflict, gin.H{"error": friendly.ErrNotYourTurn.Error()})
		return nil, "", false
	}

	return ch, actingTeamID, true
}

// announceChallenge avisa al rival que lo desafiaron. Va a los managers: el
// desafío es una decisión del equipo, no de quien lo mire primero.
func (h *FriendlyHandler) announceChallenge(ctx context.Context, ch *friendly.Challenge, from, to string) {
	if !h.notifications.Enabled() {
		return
	}
	h.notifications.EmitAsync(notify.Event{
		TeamID:   to,
		Type:     notification.TypeFriendlyChallenged,
		EntityID: ch.ID,
		Title:    "Te desafiaron a un amistoso",
		Body: teamName(ctx, h.teams, from) +
			" quiere jugar contra ustedes. Toca para ver la propuesta.",
		Recipients: notify.To(managerIDs(ctx, h.authz.memberships, to)...),
	})
}

// responseAnnouncement son los datos de un aviso de respuesta a un desafío.
// Los tres —contraoferta, aceptación y rechazo— tienen exactamente la misma
// forma: van al lado que no tocó el botón y arrancan con el nombre del que sí.
type responseAnnouncement struct {
	challenge  *friendly.Challenge
	actingTeam string
	actingUser string
	kind       notification.Type
	entityID   string
	title      string
	suffix     string
}

func (h *FriendlyHandler) announceResponse(ctx context.Context, a responseAnnouncement) {
	if !h.notifications.Enabled() {
		return
	}
	target := otherSide(a.challenge, a.actingTeam)
	h.notifications.EmitAsync(notify.Event{
		TeamID:     target,
		Type:       a.kind,
		EntityID:   a.entityID,
		Title:      a.title,
		Body:       teamName(ctx, h.teams, a.actingTeam) + a.suffix,
		Recipients: notify.To(managerIDs(ctx, h.authz.memberships, target, a.actingUser)...),
	})
}
