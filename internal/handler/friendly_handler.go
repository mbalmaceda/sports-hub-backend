package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
)

// challengeTTL es cuánto tiene el rival para responder antes de que la
// propuesta caduque. Dos días: suficiente para consultarlo con el equipo, poco
// como para no dejar la fecha bloqueada indefinidamente.
const challengeTTL = 48 * time.Hour

type FriendlyHandler struct {
	friendlies   friendly.Repository
	competitions competition.Repository
	matches      match.Repository
	authz        teamAuthorizer
}

func NewFriendlyHandler(
	friendlies friendly.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
) *FriendlyHandler {
	return &FriendlyHandler{
		friendlies:   friendlies,
		competitions: competitions,
		matches:      matches,
		authz:        teamAuthorizer{memberships: memberships},
	}
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
		ExpiresAt:        time.Now().Add(challengeTTL),
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
