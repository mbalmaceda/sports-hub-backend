package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
)

type UserHandler struct {
	repo user.Repository
}

func NewUserHandler(repo user.Repository) *UserHandler {
	return &UserHandler{repo: repo}
}

// Me GET /users/me
func (h *UserHandler) Me(c *gin.Context) {
	claims, _ := auth.ClaimsFromContext(c)
	u, err := h.repo.FindByID(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, u)
}

// UpdateProfile PATCH /users/me
// Solo actualiza los campos enviados. Campos omitidos o vacíos no sobreescriben.
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	claims, _ := auth.ClaimsFromContext(c)

	var req struct {
		Name      string `json:"name"`
		Phone     string `json:"phone"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.repo.UpdateProfile(c.Request.Context(), claims.UserID, user.ProfileUpdate{
		Name:      req.Name,
		Phone:     req.Phone,
		AvatarURL: req.AvatarURL,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update profile"})
		return
	}

	u, err := h.repo.FindByID(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, u)
}

// RegisterPushToken PUT /users/me/push-token
func (h *UserHandler) RegisterPushToken(c *gin.Context) {
	claims, _ := auth.ClaimsFromContext(c)

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.repo.UpdatePushToken(c.Request.Context(), claims.UserID, req.Token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not register push token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
