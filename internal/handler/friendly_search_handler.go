package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

// defaultSearchRadiusKm es el radio con que abre el mapa en la app. Va acá
// también para que un cliente que no lo mande vea lo mismo.
const defaultSearchRadiusKm = 5.0

/*
FriendlySearchHandler es "buscar rival": amistosos publicados sin rival que
cualquier equipo cercano puede pedir jugar.

Lo que hay que tener presente al tocarlo es una sola regla: un pedido es un
desafío normal **con el publicador como retador**. Quien publica reservó la
cancha, y en este modelo el retador es el organizador —quien la paga y le cobra
la mitad al otro en `ensureVenueSettlement`—. Por eso el desafío que nace de un
pedido tiene la propuesta inicial hecha por el que pidió: le toca contestar al
publicador, y aceptar, contraofertar y rechazar pasan por los endpoints de
siempre (`/friendlies/:id/...`), sin saber de dónde vino el desafío.
*/
type FriendlySearchHandler struct {
	searches      friendly.SearchRepository
	friendlies    friendly.Repository
	competitions  competition.Repository
	venues        venue.Repository
	teams         team.Repository
	notifications *notify.Service
	authz         teamAuthorizer
}

func NewFriendlySearchHandler(
	searches friendly.SearchRepository,
	friendlies friendly.Repository,
	competitions competition.Repository,
	venues venue.Repository,
	memberships membership.Repository,
	teams team.Repository,
	notifications *notify.Service,
) *FriendlySearchHandler {
	return &FriendlySearchHandler{
		searches:      searches,
		friendlies:    friendlies,
		competitions:  competitions,
		venues:        venues,
		teams:         teams,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

// parseCoordinate lee lat/lng de la query. Sin punto no hay "cerca de".
func parseCoordinate(c *gin.Context) (float64, float64, bool) {
	lat, errLat := strconv.ParseFloat(c.Query("lat"), 64)
	lng, errLng := strconv.ParseFloat(c.Query("lng"), 64)
	if errLat != nil || errLng != nil || !venue.ValidCoordinate(lat, lng) {
		return 0, 0, false
	}
	return lat, lng, true
}

/*
ListNearby GET /friendly-searches?team_id&lat&lng&radius_km

El punto es el del teléfono de quien mira, no el de su equipo: la pregunta es
"¿quién está buscando rival cerca de donde estoy?".

Exige ser del equipo de `team_id` —cualquier rol, sin invitados— porque el
listado contesta además si ese equipo ya pidió cada búsqueda. Un jugador ve lo
mismo que su manager; lo que no puede es pedir (ver `Request`).
*/
func (h *FriendlySearchHandler) ListNearby(c *gin.Context) {
	teamID := c.Query("team_id")
	if teamID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_id is required"})
		return
	}
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	lat, lng, ok := parseCoordinate(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lng are required"})
		return
	}

	radius := defaultSearchRadiusKm
	if raw := c.Query("radius_km"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "radius_km must be a positive number"})
			return
		}
		radius = min(parsed, friendly.MaxSearchRadiusKm)
	}

	items, err := h.searches.ListNearby(c.Request.Context(), lat, lng, radius, teamID, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list friendly searches"})
		return
	}
	if items == nil {
		items = []*friendly.Search{}
	}
	c.JSON(http.StatusOK, items)
}

// Publish POST /teams/:id/friendly-searches
func (h *FriendlySearchHandler) Publish(c *gin.Context) {
	teamID := c.Param("id")
	m, err := h.authz.requireRole(c, teamID, membership.RoleManager)
	if abortAuthz(c, err) {
		return
	}

	var req struct {
		SportID        string    `json:"sport_id"         binding:"required"`
		PlayersPerSide *int      `json:"players_per_side" binding:"omitempty,min=1,max=30"`
		StartAt        time.Time `json:"start_at"         binding:"required"`
		VenueID        string    `json:"venue_id"         binding:"required"`
		Message        string    `json:"message"`
		VenueCost      *struct {
			Amount   int64  `json:"amount"   binding:"min=0"`
			Currency string `json:"currency" binding:"required"`
		} `json:"venue_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !req.StartAt.After(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "match date must be in the future"})
		return
	}

	ctx := c.Request.Context()
	v, err := h.venues.FindByID(ctx, req.VenueID)
	if errors.Is(err, venue.ErrNotFound) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	s := &friendly.Search{
		TeamID:         teamID,
		CreatedBy:      m.UserID,
		SportID:        req.SportID,
		PlayersPerSide: req.PlayersPerSide,
		StartAt:        req.StartAt,
		Venue:          *v,
		Message:        req.Message,
		Status:         friendly.SearchOpen,
		ExpiresAt:      responseDeadline(time.Now(), friendly.SearchTTL, &req.StartAt),
	}
	if req.VenueCost != nil {
		s.VenueCost = &competition.VenueCost{Amount: req.VenueCost.Amount, Currency: req.VenueCost.Currency}
	}
	if err := h.searches.Create(ctx, s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not publish the friendly"})
		return
	}
	s.TeamName = teamName(ctx, h.teams, teamID)

	c.JSON(http.StatusCreated, s)
}

// Cancel POST /friendly-searches/:searchId/cancel
//
// Lo baja el publicador. Los pedidos que estaban esperando quedan rechazados,
// con aviso: si no, el que pidió se queda esperando una respuesta que no va a
// llegar.
func (h *FriendlySearchHandler) Cancel(c *gin.Context) {
	ctx := c.Request.Context()
	s, ok := h.loadSearch(c)
	if !ok {
		return
	}
	if _, err := h.authz.requireRole(c, s.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	declined, err := h.searches.Close(ctx, s.ID, friendly.SearchCancelled, "")
	if errors.Is(err, friendly.ErrSearchClosed) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not cancel the friendly"})
		return
	}

	actor, _ := currentUserID(c)
	releaseDeclinedRequests(ctx, h.competitions, h.notifications, h.teams, h.authz.memberships,
		declined, s.TeamID, actor, " canceló el amistoso que había publicado.")

	s.Status = friendly.SearchCancelled
	c.JSON(http.StatusOK, s)
}

/*
Request POST /friendly-searches/:searchId/requests

Pedir jugar una búsqueda. Nace la competencia (en borrador, organizada por el
publicador) y el desafío con su primera propuesta, que son la fecha y la cancha
publicadas más el mensaje de quien pide.

El retador es el publicador y la propuesta es del que pide: así le toca
contestar al publicador, y al aceptar la liquidación sale con el acreedor
correcto. Ver el comentario del handler.
*/
func (h *FriendlySearchHandler) Request(c *gin.Context) {
	var req struct {
		TeamID  string `json:"team_id" binding:"required"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.authz.requireRole(c, req.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	ctx := c.Request.Context()
	s, ok := h.loadSearch(c)
	if !ok {
		return
	}
	if s.TeamID == req.TeamID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot request your own friendly"})
		return
	}
	if !s.IsOpen(time.Now()) {
		c.JSON(http.StatusConflict, gin.H{"error": friendly.ErrSearchClosed.Error()})
		return
	}

	/*
		El índice único de la 013 es la garantía; esto es para no dejar basura.

		Sin la consulta previa, el segundo toque crearía la competencia antes de
		que el insert del desafío choque con el índice, y esa competencia huérfana
		le aparecería al publicador en el historial como un partido cancelado que
		nunca existió.
	*/
	if existing := h.openRequestFrom(ctx, s.ID, req.TeamID); existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":        friendly.ErrAlreadyRequested.Error(),
			"challenge_id": existing.ID,
		})
		return
	}

	hostName := teamName(ctx, h.teams, s.TeamID)
	requesterName := teamName(ctx, h.teams, req.TeamID)

	comp := &competition.Competition{
		SportID:         s.SportID,
		Type:            competition.TypeFriendly,
		Name:            hostName + " vs " + requesterName,
		OrganizerTeamID: s.TeamID,
		Status:          competition.StatusDraft,
		StartAt:         &s.StartAt,
		Venue:           s.Venue.Name,
		VenueID:         s.Venue.ID,
		PlayersPerSide:  s.PlayersPerSide,
		VenueCost:       s.VenueCost,
	}
	if err := h.competitions.Create(ctx, comp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create competition"})
		return
	}

	ch := &friendly.Challenge{
		CompetitionID:    comp.ID,
		ChallengerTeamID: s.TeamID,
		ChallengedTeamID: req.TeamID,
		Status:           friendly.StatusPending,
		// El pedido no vive más que su búsqueda: aceptarlo pasa por `Close`,
		// que no cierra una búsqueda vencida, y un desafío que la sobreviviera
		// le ofrecería al publicador un "Aceptar" que el servidor rechaza.
		ExpiresAt: responseDeadline(time.Now(), challengeTTL, &s.ExpiresAt),
		SearchID:  s.ID,
	}
	first := &friendly.Proposal{
		ProposedByTeamID: req.TeamID,
		ProposedStartAt:  s.StartAt,
		ProposedVenue:    s.Venue.Name,
		Message:          req.Message,
	}
	if err := h.friendlies.Create(ctx, ch, first); err != nil {
		// La competencia ya existe y no tiene desafío: se cancela para que no
		// quede como un borrador fantasma en la bandeja del publicador.
		_ = h.competitions.UpdateStatus(ctx, comp.ID, competition.StatusCancelled)
		if errors.Is(err, friendly.ErrAlreadyRequested) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not request the friendly"})
		return
	}
	ch.LastProposedByTeamID = req.TeamID

	if h.notifications.Enabled() {
		actor, _ := currentUserID(c)
		h.notifications.EmitAsync(notify.Event{
			TeamID:     s.TeamID,
			Type:       notification.TypeFriendlyRequested,
			EntityID:   ch.ID,
			Title:      "Quieren jugar tu amistoso",
			Body:       requesterName + " pidió jugar el amistoso que publicaste. Toca para responder.",
			Recipients: notify.To(managerIDs(ctx, h.authz.memberships, s.TeamID, actor)...),
		})
	}

	c.JSON(http.StatusCreated, gin.H{"competition": comp, "challenge": ch, "proposal": first})
}

func (h *FriendlySearchHandler) loadSearch(c *gin.Context) (*friendly.Search, bool) {
	s, err := h.searches.FindByID(c.Request.Context(), c.Param("searchId"))
	if errors.Is(err, friendly.ErrSearchNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	return s, true
}

// openRequestFrom devuelve el pedido abierto del equipo a esa búsqueda, si lo
// tiene. Un error al leer se trata como "no hay": el índice único sigue ahí.
func (h *FriendlySearchHandler) openRequestFrom(ctx context.Context, searchID, teamID string) *friendly.Challenge {
	items, err := h.friendlies.ListByTeam(ctx, teamID)
	if err != nil {
		return nil
	}
	for _, ch := range items {
		if ch.SearchID == searchID && ch.ChallengedTeamID == teamID && ch.Status.IsOpen() {
			return ch
		}
	}
	return nil
}

/*
releaseDeclinedRequests termina lo que `SearchRepository.Close` dejó a medias a
propósito: cancela la competencia de cada pedido rechazado y le avisa al que
pidió.

Lo usan las dos formas de cerrar una búsqueda —el publicador la baja, o acepta
a otro— y por eso vive suelta y no en un handler. Como el resto de los avisos,
es best-effort: un pedido cuya competencia no se pudo cancelar ya quedó
`declined`, y la app lo reparte bien igual.
*/
func releaseDeclinedRequests(
	ctx context.Context,
	competitions competition.Repository,
	notifications *notify.Service,
	teams team.Repository,
	memberships membership.Repository,
	declined []*friendly.Challenge,
	hostTeamID, actor, suffix string,
) {
	if len(declined) == 0 {
		return
	}
	host := teamName(ctx, teams, hostTeamID)
	for _, ch := range declined {
		if err := competitions.UpdateStatus(ctx, ch.CompetitionID, competition.StatusCancelled); err != nil {
			slog.Error("could not cancel the competition of a declined request",
				"error", err, "competition_id", ch.CompetitionID)
		}
		if !notifications.Enabled() {
			continue
		}
		requester := ch.Opponent(hostTeamID)
		notifications.EmitAsync(notify.Event{
			TeamID:     requester,
			Type:       notification.TypeFriendlyDeclined,
			EntityID:   ch.ID,
			Title:      "Tu solicitud de amistoso no siguió",
			Body:       host + suffix,
			Recipients: notify.To(managerIDs(ctx, memberships, requester, actor)...),
		})
	}
}
