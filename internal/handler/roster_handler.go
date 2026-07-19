package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
)

type RosterHandler struct {
	repo membership.Repository
}

func NewRosterHandler(repo membership.Repository) *RosterHandler {
	return &RosterHandler{repo: repo}
}

func (h *RosterHandler) ListByTeam(c *gin.Context) {
	members, err := h.repo.ListByTeam(c.Request.Context(), c.Param("id"))
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
	c.JSON(http.StatusOK, m)
}

func (h *RosterHandler) AddMember(c *gin.Context) {
	var req struct {
		UserID       string          `json:"user_id"       binding:"required"`
		Role         membership.Role `json:"role"`
		JerseyNumber *int            `json:"jersey_number"`
		Position     string          `json:"position"`
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

	m := &membership.Membership{
		UserID:       req.UserID,
		TeamID:       c.Param("id"),
		Role:         role,
		Status:       membership.StatusActive,
		JerseyNumber: req.JerseyNumber,
		Position:     req.Position,
	}
	if err := h.repo.Create(c.Request.Context(), m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not add member"})
		return
	}

	// Retorna el TeamMember completo (con datos del user)
	member, err := h.repo.GetMemberByID(c.Request.Context(), m.ID)
	if err != nil {
		c.JSON(http.StatusCreated, m)
		return
	}
	c.JSON(http.StatusCreated, member)
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

	member, err := h.repo.GetMemberByID(c.Request.Context(), c.Param("membershipId"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, member)
}
