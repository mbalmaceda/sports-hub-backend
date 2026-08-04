package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

type FeeHandler struct {
	fees        fee.Repository
	memberships membership.Repository
	teams       team.Repository
}

func NewFeeHandler(fees fee.Repository, memberships membership.Repository, teams team.Repository) *FeeHandler {
	return &FeeHandler{fees: fees, memberships: memberships, teams: teams}
}

// ListByTeamAndPeriod GET /teams/:id/fees?year=2026&month=7
func (h *FeeHandler) ListByTeamAndPeriod(c *gin.Context) {
	year, err := strconv.Atoi(c.Query("year"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year is required"})
		return
	}
	month, err := strconv.Atoi(c.Query("month"))
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month must be between 1 and 12"})
		return
	}

	obligations, err := h.fees.ListByTeamAndPeriod(c.Request.Context(), c.Param("id"), year, month)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list fees"})
		return
	}
	if obligations == nil {
		obligations = []*fee.Obligation{}
	}
	c.JSON(http.StatusOK, obligations)
}

// ListByMembership GET /memberships/:membershipId/fees
func (h *FeeHandler) ListByMembership(c *gin.Context) {
	obligations, err := h.fees.ListByMembership(c.Request.Context(), c.Param("membershipId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list fees"})
		return
	}
	if obligations == nil {
		obligations = []*fee.Obligation{}
	}
	c.JSON(http.StatusOK, obligations)
}

// GetByID GET /fees/:id
func (h *FeeHandler) GetByID(c *gin.Context) {
	o, err := h.fees.FindByID(c.Request.Context(), c.Param("id"))
	if errors.Is(err, fee.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "fee not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, o)
}

// UpdateStatus PATCH /fees/:id/status
func (h *FeeHandler) UpdateStatus(c *gin.Context) {
	var req struct {
		Status fee.Status `json:"status" binding:"required"`
		PaidAt *time.Time `json:"paid_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	paidAt := req.PaidAt
	if req.Status == fee.StatusPaid && paidAt == nil {
		now := time.Now()
		paidAt = &now
	}

	if err := h.fees.UpdateStatus(c.Request.Context(), c.Param("id"), req.Status, paidAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update status"})
		return
	}

	o, err := h.fees.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, o)
}

// Generate POST /teams/:id/fees/generate
// Genera cuotas mensuales para todos los miembros activos del equipo.
// Es idempotente: si ya existen para ese período, las omite.
func (h *FeeHandler) Generate(c *gin.Context) {
	var req struct {
		PeriodYear  int `json:"period_year"  binding:"required"`
		PeriodMonth int `json:"period_month" binding:"required,min=1,max=12"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t, err := h.teams.FindByID(c.Request.Context(), c.Param("id"))
	if errors.Is(err, team.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	members, err := h.memberships.ListByTeam(c.Request.Context(), t.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list members"})
		return
	}

	dueDate := time.Date(req.PeriodYear, time.Month(req.PeriodMonth), t.FeeDueDay, 0, 0, 0, 0, time.UTC)

	obligations := make([]*fee.Obligation, 0, len(members))
	for _, m := range members {
		// La cuota la paga quien juega. Antes se generaba para todo miembro
		// activo —también para el manager que solo dirige, que después la app
		// escondía de la lista de cuotas y quedaba como deuda fantasma.
		if m.Status != "active" || !m.PlaysAsPlayer {
			continue
		}
		obligations = append(obligations, &fee.Obligation{
			TeamID:       t.ID,
			MembershipID: m.MembershipID,
			PeriodYear:   req.PeriodYear,
			PeriodMonth:  req.PeriodMonth,
			Amount:       t.FeeAmount,
			Currency:     t.Currency,
			DueDate:      dueDate,
			Status:       fee.StatusPending,
		})
	}

	if len(obligations) == 0 {
		c.JSON(http.StatusOK, gin.H{"created": 0, "message": "no active members"})
		return
	}

	created, err := h.fees.BulkCreate(c.Request.Context(), obligations)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not generate fees"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"created": created,
		"skipped": len(obligations) - created,
		"message": fmt.Sprintf("generated fees for %s %d/%02d", t.Name, req.PeriodYear, req.PeriodMonth),
	})
}
