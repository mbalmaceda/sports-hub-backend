package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/payment"
)

type PaymentHandler struct {
	payments payment.Repository
	fees     fee.Repository
	authz    teamAuthorizer
}

func NewPaymentHandler(payments payment.Repository, fees fee.Repository, memberships membership.Repository) *PaymentHandler {
	return &PaymentHandler{payments: payments, fees: fees, authz: teamAuthorizer{memberships: memberships}}
}

// authorizeRead deja ver un pago a quien maneja la plata del equipo y, si el
// pago es de una cuota, también al dueño de esa cuota: es su propio pago.
func (h *PaymentHandler) authorizeRead(c *gin.Context, p *payment.Payment) bool {
	if p.ObligationID == "" {
		_, err := h.authz.requireFinances(c, p.TeamID)
		return !abortAuthz(c, err)
	}
	o, err := h.fees.FindByID(c.Request.Context(), p.ObligationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}
	return !abortAuthz(c, h.authz.requireSelfOrFinances(c, o.TeamID, o.MembershipID))
}

// ListByTeam GET /teams/:id/payments
func (h *PaymentHandler) ListByTeam(c *gin.Context) {
	if _, err := h.authz.requireFinances(c, c.Param("id")); abortAuthz(c, err) {
		return
	}

	payments, err := h.payments.ListByTeam(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list payments"})
		return
	}
	if payments == nil {
		payments = []*payment.Payment{}
	}
	c.JSON(http.StatusOK, payments)
}

// GetByID GET /payments/:id
func (h *PaymentHandler) GetByID(c *gin.Context) {
	p, err := h.payments.FindByID(c.Request.Context(), c.Param("id"))
	if errors.Is(err, payment.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !h.authorizeRead(c, p) {
		return
	}
	c.JSON(http.StatusOK, p)
}

// GetByObligationID GET /fees/:id/payment
func (h *PaymentHandler) GetByObligationID(c *gin.Context) {
	// Se autoriza contra la cuota y no contra el pago: así quien no puede ver la
	// cuota tampoco se entera de si existe un pago.
	o, err := h.fees.FindByID(c.Request.Context(), c.Param("id"))
	if errors.Is(err, fee.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "fee not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if abortAuthz(c, h.authz.requireSelfOrFinances(c, o.TeamID, o.MembershipID)) {
		return
	}

	p, err := h.payments.FindByObligationID(c.Request.Context(), o.ID)
	if errors.Is(err, payment.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no payment found for this obligation"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Record POST /teams/:id/payments
func (h *PaymentHandler) Record(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireFinances(c, teamID); abortAuthz(c, err) {
		return
	}
	claims, _ := auth.ClaimsFromContext(c)

	var req struct {
		ObligationID string         `json:"obligation_id"`
		PayerID      string         `json:"payer_id"   binding:"required"`
		Amount       int64          `json:"amount"     binding:"required"`
		Currency     string         `json:"currency"   binding:"required"`
		Method       payment.Method `json:"method"     binding:"required"`
		Notes        string         `json:"notes"`
		ReceiptURL   string         `json:"receipt_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// La cuota tiene que ser de este equipo: sin esto, el tesorero de un equipo
	// marca como pagada la cuota de otro.
	if req.ObligationID != "" {
		o, err := h.fees.FindByID(c.Request.Context(), req.ObligationID)
		if errors.Is(err, fee.ErrNotFound) || (err == nil && o.TeamID != teamID) {
			c.JSON(http.StatusNotFound, gin.H{"error": "fee not found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	p := &payment.Payment{
		TeamID:       teamID,
		ObligationID: req.ObligationID,
		PayerID:      req.PayerID,
		RecordedBy:   claims.UserID,
		Amount:       req.Amount,
		Currency:     req.Currency,
		Method:       req.Method,
		Notes:        req.Notes,
		ReceiptURL:   req.ReceiptURL,
	}

	if err := h.payments.Create(c.Request.Context(), p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not record payment"})
		return
	}

	// Si el pago está ligado a una cuota, la marca como pagada automáticamente
	if req.ObligationID != "" {
		_ = h.fees.UpdateStatus(c.Request.Context(), req.ObligationID, fee.StatusPaid, &p.CreatedAt)
	}

	c.JSON(http.StatusCreated, p)
}

// Reverse POST /payments/:id/reverse
func (h *PaymentHandler) Reverse(c *gin.Context) {
	p, err := h.payments.FindByID(c.Request.Context(), c.Param("id"))
	if errors.Is(err, payment.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if _, err := h.authz.requireFinances(c, p.TeamID); abortAuthz(c, err) {
		return
	}
	if p.IsReversed {
		c.JSON(http.StatusConflict, gin.H{"error": "payment already reversed"})
		return
	}

	if err := h.payments.Reverse(c.Request.Context(), p.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not reverse payment"})
		return
	}

	// Si tenía una cuota asociada, la vuelve a pending
	if p.ObligationID != "" {
		_ = h.fees.UpdateStatus(c.Request.Context(), p.ObligationID, fee.StatusPending, nil)
	}

	p.IsReversed = true
	c.JSON(http.StatusOK, p)
}
