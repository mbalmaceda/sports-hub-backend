package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

type TeamHandler struct {
	repo team.Repository
}

func NewTeamHandler(repo team.Repository) *TeamHandler {
	return &TeamHandler{repo: repo}
}

func (h *TeamHandler) Create(c *gin.Context) {
	var req struct {
		Name      string `json:"name"       binding:"required"`
		SportID   string `json:"sport_id"   binding:"required"`
		Category  string `json:"category"   binding:"required"`
		ClubID    string `json:"club_id"`
		LogoURL   string `json:"logo_url"`
		FeeAmount int64  `json:"fee_amount"`
		FeeDueDay int    `json:"fee_due_day" binding:"min=1,max=31"`
		Currency  string `json:"currency"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t := &team.Team{
		Name:      req.Name,
		SportID:   req.SportID,
		Category:  req.Category,
		ClubID:    req.ClubID,
		LogoURL:   req.LogoURL,
		FeeAmount: req.FeeAmount,
		FeeDueDay: req.FeeDueDay,
		Currency:  req.Currency,
		IsActive:  true,
	}
	if err := h.repo.Create(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create team"})
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *TeamHandler) GetByID(c *gin.Context) {
	t, err := h.repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *TeamHandler) List(c *gin.Context) {
	teams, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list teams"})
		return
	}
	if teams == nil {
		teams = []*team.Team{}
	}
	c.JSON(http.StatusOK, teams)
}

func (h *TeamHandler) UpdateFeeConfig(c *gin.Context) {
	var req struct {
		FeeAmount int64 `json:"fee_amount" binding:"required"`
		FeeDueDay int   `json:"fee_due_day" binding:"required,min=1,max=31"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.repo.UpdateFeeConfig(c.Request.Context(), c.Param("id"), team.FeeConfig{
		FeeAmount: req.FeeAmount,
		FeeDueDay: req.FeeDueDay,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update fee config"})
		return
	}

	t, err := h.repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, t)
}
