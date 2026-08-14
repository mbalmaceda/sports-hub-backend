package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
)

type RosterHandler struct {
	repo     membership.Repository
	firebase *firebase.Firebase
	authz    teamAuthorizer
}

func NewRosterHandler(repo membership.Repository, fb *firebase.Firebase) *RosterHandler {
	return &RosterHandler{
		repo:     repo,
		firebase: fb,
		authz:    teamAuthorizer{memberships: repo},
	}
}

// syncMirror refleja en Firestore lo que quedó guardado en Postgres.
//
// Vuelve a leer la membresía en vez de reutilizar lo que traía el request: el
// espejo tiene que copiar lo que de verdad se guardó, no lo que se pidió
// guardar. Sin Firebase configurado no hace nada.
func (h *RosterHandler) syncMirror(ctx context.Context, membershipID string) {
	if !h.firebase.Enabled() {
		return
	}
	member, err := h.repo.GetMemberByID(ctx, membershipID)
	if err != nil {
		slog.Error("mirror: could not read membership", "error", err, "membership_id", membershipID)
		return
	}
	// Va sin MatchID a propósito. Este camino es el de la gestión del plantel, y
	// no sabe a qué partido entró un invitado; escribir el espejo sin ese dato
	// le CIERRA la lectura en vivo en vez de abrirla, que es el lado seguro para
	// equivocarse. El API sigue respondiéndole igual.
	h.firebase.SyncMembershipAsync(firebase.Membership{
		TeamID: member.TeamID,
		UserID: member.UserID,
		Role:   string(member.Role),
		Status: string(member.Status),
		Kind:   string(member.Kind),
	})
}

// ListByTeam GET /teams/:id/roster
//
// El plantel es de quien está adentro. Sin esta guarda alcanzaba con tener
// sesión y el id de un equipo para leer su plantel entero, sin pertenecer:
// el middleware de auth prueba que el JWT es válido y nada más.
//
// El listado no trae email ni teléfono —eso vive en la ficha individual, ver
// `rosterListQuery`—, así que lo que devuelve es quién juega en el equipo y no
// cómo contactarlo.
func (h *RosterHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	members, err := h.repo.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list roster"})
		return
	}
	if members == nil {
		members = []*membership.TeamMember{}
	}
	c.JSON(http.StatusOK, members)
}

// ListMine GET /me/memberships
// Lista los equipos (y el rol en cada uno) del usuario autenticado.
// El mobile la usa para resolver la sesión activa tras login/refresh.
func (h *RosterHandler) ListMine(c *gin.Context) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	members, err := h.repo.ListByUser(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list memberships"})
		return
	}
	if members == nil {
		members = []*membership.TeamMember{}
	}
	c.JSON(http.StatusOK, members)
}

// GetMember GET /memberships/:membershipId (y /teams/:id/roster/:membershipId)
//
// Ficha completa de un jugador: es la única que devuelve email y teléfono, así
// que el chequeo de pertenencia importa más acá que en el listado.
//
// Se lee primero y se autoriza después porque el equipo al que hay que
// pertenecer sale de la propia membresía: la ruta corta no lo trae en el path.
// No se filtra nada por leer antes, la respuesta sigue saliendo por un solo
// lugar.
func (h *RosterHandler) GetMember(c *gin.Context) {
	m, err := h.repo.GetMemberByID(c.Request.Context(), c.Param("membershipId"))
	if errors.Is(err, membership.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireMember(c, m.TeamID); abortAuthz(c, err) {
		return
	}

	c.JSON(http.StatusOK, m)
}

func (h *RosterHandler) AddMember(c *gin.Context) {
	var req struct {
		UserID        string          `json:"user_id"       binding:"required"`
		Role          membership.Role `json:"role"`
		PlaysAsPlayer *bool           `json:"plays_as_player"`
		JerseyNumber  *int            `json:"jersey_number"`
		Position      string          `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		role = membership.RolePlayer
	}
	if role != membership.RolePlayer && role != membership.RoleTreasurer && role != membership.RoleManager {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
		return
	}

	// Evitar duplicados
	existing, err := h.repo.FindByUserAndTeam(c.Request.Context(), req.UserID, c.Param("id"))
	if err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "user is already a member of this team"})
		return
	}

	playsAsPlayer := membership.DefaultPlaysAsPlayer(role)
	if req.PlaysAsPlayer != nil {
		playsAsPlayer = *req.PlaysAsPlayer
	}

	m := &membership.Membership{
		UserID:        req.UserID,
		TeamID:        c.Param("id"),
		Role:          role,
		PlaysAsPlayer: playsAsPlayer,
		Status:        membership.StatusActive,
		JerseyNumber:  req.JerseyNumber,
		Position:      req.Position,
	}
	if err := h.repo.Create(c.Request.Context(), m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not add member"})
		return
	}

	h.syncMirror(c.Request.Context(), m.ID)

	// Retorna el TeamMember completo (con datos del user)
	member, err := h.repo.GetMemberByID(c.Request.Context(), m.ID)
	if err != nil {
		c.JSON(http.StatusCreated, m)
		return
	}
	c.JSON(http.StatusCreated, member)
}

// PromoteGuest POST /teams/:id/roster/:membershipId/promote
//
// Suma al plantel a un invitado que ya jugó: el final feliz del "parche".
//
// Es el momento de conversión de esta feature —alguien entró por un enlace de
// WhatsApp para un sábado y termina siendo del club— y por eso es un acto
// explícito del manager y no algo que pase solo después de N partidos: cambia lo
// que la persona ve, y le empieza a generar cuota mensual.
func (h *RosterHandler) PromoteGuest(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager); abortAuthz(c, err) {
		return
	}

	membershipID := c.Param("membershipId")
	target, err := h.repo.GetMemberByID(c.Request.Context(), membershipID)
	if errors.Is(err, membership.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	// El equipo de la ruta tiene que ser el de la membresía: sin esto, el
	// manager de un equipo asciende a un invitado de otro.
	if target.TeamID != teamID {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	if !target.IsGuest() {
		c.JSON(http.StatusConflict, gin.H{"error": "this member already belongs to the squad"})
		return
	}

	if err := h.repo.PromoteGuest(c.Request.Context(), membershipID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not promote the guest"})
		return
	}

	// El espejo lleva kind y matchId: al dejar de ser invitado hay que sacarle
	// el corte que lo encerraba en un partido, o seguiría sin ver el resto.
	h.syncMirror(c.Request.Context(), membershipID)

	member, err := h.repo.GetMemberByID(c.Request.Context(), membershipID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "promoted"})
		return
	}
	c.JSON(http.StatusOK, member)
}

func (h *RosterHandler) UpdateStatus(c *gin.Context) {
	var req struct {
		Status membership.Status `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.repo.UpdateStatus(c.Request.Context(), c.Param("membershipId"), req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update status"})
		return
	}

	// Dar de baja a alguien tiene que sacarle el acceso a Firestore, no solo
	// esconderle el equipo en la app.
	h.syncMirror(c.Request.Context(), c.Param("membershipId"))

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// UpdateRole PATCH /teams/:id/roster/:membershipId/role
// TODO: restringir a memberships con role=manager del equipo (ver auth.ClaimsFromContext)
// una vez que haya middleware de autorización por rol.
func (h *RosterHandler) UpdateRole(c *gin.Context) {
	var req struct {
		Role membership.Role `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role != membership.RolePlayer && req.Role != membership.RoleTreasurer && req.Role != membership.RoleManager {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
		return
	}

	if err := h.repo.UpdateRole(c.Request.Context(), c.Param("membershipId"), req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update role"})
		return
	}

	// El motivo de que los roles no viajen en el token: acá el cambio llega al
	// espejo enseguida, en vez de esperar a que expire la sesión de nadie.
	h.syncMirror(c.Request.Context(), c.Param("membershipId"))

	member, err := h.repo.GetMemberByID(c.Request.Context(), c.Param("membershipId"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, member)
}
