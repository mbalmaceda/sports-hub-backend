package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
)

// tokenBytes son 32 bytes = 256 bits de entropía.
//
// El enlace es una credencial al portador que viaja por WhatsApp y vale para
// entrar a un equipo ajeno: tiene que ser imposible de adivinar, no corto.
const tokenBytes = 32

// maxGuestsPerInvite es el techo de un enlace, sin importar cuántos falten.
//
// Existe porque el cupo lo propone el cliente y un `max_uses` de 500 convertiría
// el enlace en una puerta abierta al equipo. Once es un plantel entero: si
// faltan más que eso, el problema no lo resuelve un link.
const maxGuestsPerInvite = 11

type GuestHandler struct {
	invites      guest.Repository
	matches      match.Repository
	memberships  membership.Repository
	competitions competition.Repository
	charges      charge.Repository
	teams        team.Repository
	users        user.Repository
	firebase     *firebase.Firebase
	cfg          config.Config
	authz        teamAuthorizer
}

func NewGuestHandler(
	invites guest.Repository,
	matches match.Repository,
	memberships membership.Repository,
	competitions competition.Repository,
	charges charge.Repository,
	teams team.Repository,
	users user.Repository,
	fb *firebase.Firebase,
	cfg config.Config,
) *GuestHandler {
	return &GuestHandler{
		invites:      invites,
		matches:      matches,
		memberships:  memberships,
		competitions: competitions,
		charges:      charges,
		teams:        teams,
		users:        users,
		firebase:     fb,
		cfg:          cfg,
		authz:        teamAuthorizer{memberships: memberships},
	}
}

// newInviteToken devuelve el token en claro y su hash. El claro se ve una sola
// vez, en la respuesta de creación; después solo queda el hash.
func newInviteToken() (string, string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

/*
CreateInvite POST /matches/:matchId/guest-invites

Genera el enlace que el manager comparte por WhatsApp para completar una
convocatoria con gente de afuera.

El cupo lo manda el cliente porque es él quien sabe cuántos faltan —la nómina
sale de la competencia y de las respuestas— pero el servidor lo acota: entre 1 y
maxGuestsPerInvite. El vencimiento no se negocia y es la hora del partido:
después de eso el enlace no sirve para nada y no tiene por qué seguir vivo.
*/
func (h *GuestHandler) CreateInvite(c *gin.Context) {
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
		TeamID  string `json:"team_id"  binding:"required"`
		MaxUses int    `json:"max_uses" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !m.Involves(req.TeamID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "that team does not play this match"})
		return
	}

	// El permiso va antes que el resto de las validaciones: a quien no tiene
	// nada que hacer acá no se le contesta si el cuerpo estaba bien o mal.
	// Sumar gente al partido es convocar, así que es el mismo que la citación.
	me, err := h.authz.requireRole(c, req.TeamID, membership.RoleManager)
	if abortAuthz(c, err) {
		return
	}

	if req.MaxUses > maxGuestsPerInvite {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_uses is too high"})
		return
	}

	// Un partido que ya empezó no admite refuerzos, y un enlace que nace
	// vencido es un botón que no hace nada.
	if !time.Now().Before(m.ScheduledAt) {
		c.JSON(http.StatusConflict, gin.H{"error": "this match already started"})
		return
	}

	token, tokenHash, err := newInviteToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create the invitation"})
		return
	}

	inv := &guest.Invite{
		MatchID:         m.ID,
		TeamID:          req.TeamID,
		CreatedByUserID: me.UserID,
		MaxUses:         req.MaxUses,
		ExpiresAt:       m.ScheduledAt,
	}
	if err := h.invites.Create(c.Request.Context(), inv, tokenHash); err != nil {
		slog.Error("could not create the guest invitation", "error", err, "match_id", m.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create the invitation"})
		return
	}

	/*
		La URL la arma el servidor, no el cliente.

		El cliente no sabe con qué dominio se sirven las invitaciones —y el que
		tiene a mano es el esquema de la app, `zports://`, que es justamente el
		que no sirve: quien recibe el enlace no tiene la app, así que en WhatsApp
		no le abre nada y ni siquiera es tocable.

		Vacía cuando PUBLIC_BASE_URL no está configurado. El cliente tiene que
		tratarla como ausente y no compartir nada: mandar un enlace roto por
		WhatsApp se paga con la persona que lo recibió.
	*/
	var shareURL string
	if h.cfg.InviteLinksEnabled() {
		shareURL = h.cfg.PublicBaseURL + "/i/" + token
	}

	// El token va solo acá. No se puede volver a leer: si se pierde, se hace
	// otro enlace.
	c.JSON(http.StatusCreated, gin.H{
		"id":         inv.ID,
		"token":      token,
		"url":        shareURL,
		"match_id":   inv.MatchID,
		"team_id":    inv.TeamID,
		"max_uses":   inv.MaxUses,
		"used_count": inv.UsedCount,
		"expires_at": inv.ExpiresAt,
	})
}

/*
GetInvite GET /invites/:token — PÚBLICO, sin sesión.

Es la pantalla que ve alguien que todavía no tiene cuenta: por eso no puede
exigir token de sesión. Lo que devuelve es deliberadamente flaco —equipo,
cuándo, dónde, quién invita y cuánto sale— y nada más: es una URL que cualquiera
con el token puede abrir, así que todo lo que salga por acá hay que darlo por
publicado. Sin plantel, sin finanzas del equipo, sin datos de contacto de nadie.

Un enlace vencido, revocado o sin cupo devuelve 410 y no 404: la diferencia le
importa a quien lo recibió tarde, que tiene que entender que llegó tarde y no
que el link estaba mal.
*/
func (h *GuestHandler) GetInvite(c *gin.Context) {
	inv, err := h.invites.FindByTokenHash(c.Request.Context(), hashInviteToken(c.Param("token")))
	if errors.Is(err, guest.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !inv.Usable(time.Now()) {
		c.JSON(http.StatusGone, gin.H{"error": guest.ErrNotUsable.Error()})
		return
	}

	preview, ok := h.buildPreview(c, inv)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, preview)
}

// buildPreview arma lo que se muestra del partido sin exponer nada del equipo.
func (h *GuestHandler) buildPreview(c *gin.Context, inv *guest.Invite) (*guest.Preview, bool) {
	ctx := c.Request.Context()

	m, err := h.matches.FindByID(ctx, inv.MatchID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	host, err := h.teams.FindByID(ctx, inv.TeamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	inviter, err := h.users.FindByID(ctx, inv.CreatedByUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}

	preview := &guest.Preview{
		TeamName:      host.Name,
		InvitedBy:     inviter.Name,
		ScheduledAt:   m.ScheduledAt,
		Venue:         m.Venue,
		IsInternal:    m.HomeTeamID == m.AwayTeamID,
		RemainingUses: inv.RemainingUses(),
		ExpiresAt:     inv.ExpiresAt,
	}

	// El rival, solo su nombre. En un partido interno no hay ninguno.
	if !preview.IsInternal {
		rivalID := m.AwayTeamID
		if rivalID == inv.TeamID {
			rivalID = m.HomeTeamID
		}
		if rival, err := h.teams.FindByID(ctx, rivalID); err == nil {
			preview.OpponentName = rival.Name
		}
	}

	/*
		Cuánto le va a salir. Es el dato que más se agradece y el que más caro
		sale omitir: el parche que se entera en la cancha de que debe la cuota
		es una pelea, y el que queda mal es el que lo invitó.

		Sale de la competencia, no se recalcula acá: es la misma cuota fija que
		paga el resto del equipo.
	*/
	if comp, err := h.competitions.FindByID(ctx, m.CompetitionID); err == nil {
		if comp.PlayerShare != nil && comp.VenueCost != nil {
			preview.CostPerPlayer = *comp.PlayerShare
			preview.Currency = comp.VenueCost.Currency
		}
	}

	return preview, true
}

/*
Accept POST /invites/:token/accept — requiere sesión.

Canjea el enlace con la cuenta de quien lo abre. Registrarse es un paso aparte
(POST /auth/register, el de siempre): partirlo así evita un segundo camino de
alta de usuarios con su propia validación de RUT, su propio rate limit y su
propia forma de romperse.

El canje en sí es una transacción sola en el repositorio —cupo, membresía y
convocatoria—, porque cortarse en el medio deja o un cupo gastado sin nadie
adentro, o una membresía sin el partido por el que entró. Esa membresía huérfana
es la peor de las dos: es la que después cobra cuotas.
*/
func (h *GuestHandler) Accept(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	result, err := h.invites.Redeem(c.Request.Context(), hashInviteToken(c.Param("token")), userID, time.Now())
	switch {
	case errors.Is(err, guest.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	case errors.Is(err, guest.ErrNotUsable):
		c.JSON(http.StatusGone, gin.H{"error": guest.ErrNotUsable.Error()})
		return
	case errors.Is(err, guest.ErrAlreadyMember):
		c.JSON(http.StatusConflict, gin.H{"error": guest.ErrAlreadyMember.Error()})
		return
	case err != nil:
		slog.Error("could not redeem the guest invitation", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not join the match"})
		return
	}

	// Desde acá todo es efecto colateral: el canje ya está guardado y es lo que
	// la persona vino a hacer. Un fallo de Firebase o del cargo no puede
	// devolverle un error que la deje creyendo que no entró.
	m, err := h.matches.FindByID(c.Request.Context(), result.MatchID)
	if err == nil {
		// Mismo cargo que paga el resto: el parche paga la cancha por la app.
		ensureMatchCharge(c.Request.Context(), h.charges, h.competitions, m, result.TeamID, result.MembershipID)

		h.firebase.SyncCallupsAsync([]firebase.Callup{{
			TeamID:  result.TeamID,
			MatchID: result.MatchID,
			UserID:  userID,
			Status:  string(match.CallupConfirmed),
		}})
	} else {
		slog.Error("guest joined but the match could not be read",
			"error", err, "match_id", result.MatchID)
	}

	// El espejo de membresías es lo que le da lectura en Firestore; sin esto el
	// invitado entra pero no ve la convocatoria en vivo.
	//
	// Va con kind y match_id porque son los dos campos con los que las reglas
	// lo encierran en su partido: sin ellos, el espejo lo haría indistinguible
	// del plantel y pasaría a leer las convocatorias de todos los partidos del
	// equipo. Ver firestore.rules.
	h.firebase.SyncMembershipAsync(firebase.Membership{
		TeamID:  result.TeamID,
		UserID:  userID,
		Role:    string(membership.RolePlayer),
		Status:  string(membership.StatusActive),
		Kind:    string(membership.KindGuest),
		MatchID: result.MatchID,
	})

	c.JSON(http.StatusOK, result)
}

// RevokeInvite DELETE /guest-invites/:inviteId
//
// Apaga el enlace antes de tiempo. El caso es el de siempre: se mandó al grupo
// equivocado. No borra a nadie que ya haya entrado —esa gente está convocada y
// probablemente ya pagó—, solo cierra la puerta.
func (h *GuestHandler) RevokeInvite(c *gin.Context) {
	inv, err := h.invites.FindByID(c.Request.Context(), c.Param("inviteId"))
	if errors.Is(err, guest.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireRole(c, inv.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	if err := h.invites.Revoke(c.Request.Context(), inv.ID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not revoke the invitation"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

// ListInvites GET /matches/:matchId/guest-invites
//
// Los enlaces vivos de un partido, para que el manager vea cuántos lugares
// repartió antes de generar otro. Sin el token: ese ya no existe en claro.
func (h *GuestHandler) ListInvites(c *gin.Context) {
	m, err := h.matches.FindByID(c.Request.Context(), c.Param("matchId"))
	if errors.Is(err, match.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Alcanza con ser manager de cualquiera de los dos lados: cada uno ve los
	// enlaces del partido que juega.
	authorized := false
	for _, teamID := range []string{m.HomeTeamID, m.AwayTeamID} {
		if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); err == nil {
			authorized = true
			break
		}
	}
	if !authorized {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}

	invites, err := h.invites.ListByMatch(c.Request.Context(), m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list invitations"})
		return
	}

	now := time.Now()
	out := make([]gin.H, 0, len(invites))
	for _, inv := range invites {
		out = append(out, gin.H{
			"id":             inv.ID,
			"team_id":        inv.TeamID,
			"max_uses":       inv.MaxUses,
			"used_count":     inv.UsedCount,
			"remaining_uses": inv.RemainingUses(),
			"usable":         inv.Usable(now),
			"expires_at":     inv.ExpiresAt,
			"created_at":     inv.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}
