package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/teamlink"
)

/*
El enlace para unirse al equipo: lo que el manager tira al grupo de WhatsApp
para que se sumen los que todavía no tienen la app.

El cupo y el plazo no los manda el cliente. A diferencia del parche —donde la
app sabe cuántos faltan para el sábado—, acá no hay un número que el manager
conozca mejor que el servidor, y dejarlo elegir es dejar la puerta del club tan
abierta como quiera.
*/
const (
	teamLinkMaxUses = 30
	teamLinkTTL     = 7 * 24 * time.Hour
)

// teamLinkPath es el prefijo del enlace corto. No es `/i/` a propósito: la app
// 1.0.x ya abre ese prefijo como invitación a un partido y diría que el enlace
// no existe. Con uno propio, la versión vieja cae en la página web.
const teamLinkPath = "/e/"

/*
CreateTeamLink POST /teams/:id/invite-links

Genera el enlace. El token en claro va solo en esta respuesta.
*/
func (h *OnboardingHandler) CreateTeamLink(c *gin.Context) {
	teamID := c.Param("id")
	me, err := h.authz.requireRole(c, teamID, membership.RoleManager)
	if abortAuthz(c, err) {
		return
	}

	token, tokenHash, err := newInviteToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create the link"})
		return
	}

	inv := &teamlink.Invite{
		TeamID:          teamID,
		CreatedByUserID: me.UserID,
		MaxUses:         teamLinkMaxUses,
		ExpiresAt:       time.Now().Add(teamLinkTTL),
	}
	if err := h.links.Create(c.Request.Context(), inv, tokenHash); err != nil {
		slog.Error("could not create the team link", "error", err, "team_id", teamID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create the link"})
		return
	}

	// Vacía sin PUBLIC_BASE_URL, igual que en los parches: el cliente no
	// comparte nada antes que mandar un enlace roto.
	var shareURL string
	if h.cfg.InviteLinksEnabled() {
		shareURL = h.cfg.PublicBaseURL + teamLinkPath + token
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":             inv.ID,
		"token":          token,
		"url":            shareURL,
		"team_id":        inv.TeamID,
		"max_uses":       inv.MaxUses,
		"used_count":     inv.UsedCount,
		"remaining_uses": inv.RemainingUses(),
		"usable":         true,
		"expires_at":     inv.ExpiresAt,
		"created_at":     inv.CreatedAt,
	})
}

// ListTeamLinks GET /teams/:id/invite-links
//
// Los enlaces del equipo, sin token: ese ya no existe en claro.
func (h *OnboardingHandler) ListTeamLinks(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	links, err := h.links.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list links"})
		return
	}

	now := time.Now()
	out := make([]gin.H, 0, len(links))
	for _, inv := range links {
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

// RevokeTeamLink DELETE /team-invite-links/:linkId
//
// Cierra la puerta antes de tiempo —se mandó al grupo equivocado—. No saca a
// nadie que ya haya entrado.
func (h *OnboardingHandler) RevokeTeamLink(c *gin.Context) {
	inv, err := h.links.FindByID(c.Request.Context(), c.Param("linkId"))
	if errors.Is(err, teamlink.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireRole(c, inv.TeamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	if err := h.links.Revoke(c.Request.Context(), inv.ID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not revoke the link"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

/*
GetTeamLink GET /team-links/:token — PÚBLICO, sin sesión.

Lo que ve quien abre el enlace antes de tener cuenta. Igual que la vista previa
del parche, todo lo que sale por acá se da por publicado: nombre, deporte,
ciudad, la descripción que el equipo escribió para mostrarse y quién invita.

Vencido, anulado o lleno es 410 y no 404: el que llegó tarde tiene que entender
que llegó tarde, no que el link estaba mal.
*/
func (h *OnboardingHandler) GetTeamLink(c *gin.Context) {
	inv, err := h.links.FindByTokenHash(c.Request.Context(), hashInviteToken(c.Param("token")))
	if errors.Is(err, teamlink.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !inv.Usable(time.Now()) {
		c.JSON(http.StatusGone, gin.H{"error": teamlink.ErrNotUsable.Error()})
		return
	}

	t, err := h.teams.FindByID(c.Request.Context(), inv.TeamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, teamlink.Preview{
		TeamName:      t.Name,
		SportID:       t.SportID,
		City:          t.City,
		Description:   t.Description,
		InvitedBy:     h.userName(c.Request.Context(), inv.CreatedByUserID),
		RemainingUses: inv.RemainingUses(),
		ExpiresAt:     inv.ExpiresAt,
	})
}

/*
AcceptTeamLink POST /team-links/:token/accept — requiere sesión.

Canjea el enlace con la cuenta de quien lo abre. Registrarse es un paso aparte,
el de siempre, por lo mismo que en el parche: un solo camino de alta de
usuarios.

Termina igual que aceptar una invitación por correo —espejo en Firestore y aviso
al cuerpo técnico— porque para el resto del equipo el hecho es el mismo: hay
uno más. El manager que compartió el link no lo apretó, así que también recibe
el aviso: es la única forma que tiene de saber quién entró.
*/
func (h *OnboardingHandler) AcceptTeamLink(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	result, err := h.links.Redeem(c.Request.Context(), hashInviteToken(c.Param("token")), userID, time.Now())
	switch {
	case errors.Is(err, teamlink.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	case errors.Is(err, teamlink.ErrNotUsable):
		c.JSON(http.StatusGone, gin.H{"error": teamlink.ErrNotUsable.Error()})
		return
	case errors.Is(err, teamlink.ErrAlreadyMember):
		c.JSON(http.StatusConflict, gin.H{"error": teamlink.ErrAlreadyMember.Error()})
		return
	case err != nil:
		slog.Error("could not redeem the team link", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not join the team"})
		return
	}

	// Efectos colaterales: la membresía ya está guardada, y un fallo acá no
	// puede devolverle a la persona un error que la deje creyendo que no entró.
	h.syncMirror(c.Request.Context(), result.TeamID, userID)
	h.announceJoin(c.Request.Context(), result.TeamID, userID)

	c.JSON(http.StatusOK, result)
}
