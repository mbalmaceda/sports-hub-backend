package handler

import (
	"errors"
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
		Name          string     `json:"name"`
		TaxID         string     `json:"tax_id"`
		Phone         string     `json:"phone"`
		AvatarURL     string     `json:"avatar_url"`
		FavoriteSport string     `json:"favorite_sport"`
		HeightCm      *int       `json:"height_cm"`
		WeightKg      *float64   `json:"weight_kg"`
		BirthDate     *user.Date `json:"birth_date"`
		Alias         string     `json:"alias"`
		City          string     `json:"city"`
		DominantSide  string     `json:"dominant_side"`
		Bio           string     `json:"bio"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validDominantSide(req.DominantSide) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dominant_side must be one of right, left, both"})
		return
	}

	if err := h.repo.UpdateProfile(c.Request.Context(), claims.UserID, user.ProfileUpdate{
		Name:          req.Name,
		TaxID:         normalizeTaxID(req.TaxID),
		Phone:         req.Phone,
		AvatarURL:     req.AvatarURL,
		FavoriteSport: req.FavoriteSport,
		HeightCm:      req.HeightCm,
		WeightKg:      req.WeightKg,
		BirthDate:     req.BirthDate,
		Alias:         req.Alias,
		City:          req.City,
		DominantSide:  req.DominantSide,
		Bio:           req.Bio,
	}); err != nil {
		if errors.Is(err, user.ErrTaxIDTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": "tax id already registered"})
			return
		}
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
