package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
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
	authz        teamAuthorizer
}

func NewFriendlyHandler(
	friendlies friendly.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
	settlements settlement.Repository,
) *FriendlyHandler {
	return &FriendlyHandler{
		friendlies:   friendlies,
		competitions: competitions,
		matches:      matches,
		settlements:  settlements,
		authz:        teamAuthorizer{memberships: memberships},
	}
}

/*
expireStale cierra lo que se pasó de plazo: el desafío queda 'expired' y su
competencia, cancelada. Es exactamente lo que hace Decline, pero decidido por el
reloj en vez de por una persona.

Corre al leer porque no hay quién más lo corra. Lo que corresponde es un trabajo
periódico; mientras no exista, esto alcanza: quien mira la lista es justamente
quien necesita el estado al día, y una lectura que no barre deja el desafío en
'pending' para siempre. La contracara es que nada expira hasta que alguien abra
la app —si el equipo no entra, el estado guardado sigue viejo—.

Los errores se tragan a propósito. La barrida es mantenimiento, no la respuesta:
si falla, el cliente ve un estado viejo, que es exactamente lo que veía antes de
que esto existiera. Voltear la lectura por eso sería cambiar un dato desactualizado
por una pantalla de error.
*/
func (h *FriendlyHandler) expireStale(c *gin.Context) {
	ctx := c.Request.Context()

	competitionIDs, err := h.friendlies.ExpireStale(ctx, time.Now())
	if err != nil {
		return
	}
	for _, id := range competitionIDs {
		_ = h.competitions.UpdateStatus(ctx, id, competition.StatusCancelled)
	}
}

// ListByTeam GET /teams/:id/friendlies
func (h *FriendlyHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	h.expireStale(c)

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
	h.expireStale(c)

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

	c.JSON(http.StatusCreated, p)
}

// Accept POST /friendlies/:challengeId/accept
// Cierra la negociación y crea el partido confirmado con la última propuesta.
func (h *FriendlyHandler) Accept(c *gin.Context) {
	ch, _, ok := h.loadForResponse(c)
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

	ch.Status = friendly.StatusAccepted
	c.JSON(http.StatusOK, gin.H{"challenge": ch, "match": m})
}

// Decline POST /friendlies/:challengeId/decline
func (h *FriendlyHandler) Decline(c *gin.Context) {
	ch, _, ok := h.loadForResponse(c)
	if !ok {
		return
	}

	if err := h.friendlies.UpdateStatus(c.Request.Context(), ch.ID, friendly.StatusDeclined); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not decline friendly"})
		return
	}
	_ = h.competitions.UpdateStatus(c.Request.Context(), ch.CompetitionID, competition.StatusCancelled)

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
