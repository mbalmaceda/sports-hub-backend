package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

type ChargeHandler struct {
	charges       charge.Repository
	competitions  competition.Repository
	matches       match.Repository
	memberships   membership.Repository
	funds         funds.Repository
	notifications *notification.Service
	authz         teamAuthorizer
}

func NewChargeHandler(
	charges charge.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
	funds funds.Repository,
	notifications *notification.Service,
) *ChargeHandler {
	return &ChargeHandler{
		charges:       charges,
		competitions:  competitions,
		matches:       matches,
		memberships:   memberships,
		funds:         funds,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

// ListBySource GET /competitions/:competitionId/charges
func (h *ChargeHandler) ListByCompetition(c *gin.Context) {
	charges, err := h.charges.ListBySource(c.Request.Context(), charge.Source{
		Type: charge.SourceMatchCost,
		ID:   c.Param("competitionId"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list charges"})
		return
	}
	if charges == nil {
		charges = []*charge.Charge{}
	}
	if len(charges) > 0 {
		if _, err := h.authz.requireMember(c, charges[0].TeamID); abortAuthz(c, err) {
			return
		}
	}
	c.JSON(http.StatusOK, charges)
}

// ListByTeamAndPeriod GET /teams/:id/charges?year=2026&month=8
//
// Lo que el equipo cobró y tiene por cobrar en un mes. Es la otra mitad del
// ingreso: sin esto el resumen mensual solo veía las cuotas, y desde que los
// gastos pueden colgar de un partido restaba la cancha sin sumar nunca lo que
// los jugadores pagaron por ella.
func (h *ChargeHandler) ListByTeamAndPeriod(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	year, month, ok := periodFromQuery(c)
	if !ok {
		return
	}

	charges, err := h.charges.ListByTeamAndPeriod(c.Request.Context(), teamID, year, month)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list charges"})
		return
	}
	if charges == nil {
		charges = []*charge.Charge{}
	}
	c.JSON(http.StatusOK, charges)
}

// ListByMembership GET /memberships/:membershipId/charges
func (h *ChargeHandler) ListByMembership(c *gin.Context) {
	membershipID := c.Param("membershipId")

	target, err := h.memberships.GetMemberByID(c.Request.Context(), membershipID)
	if errors.Is(err, membership.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "membership not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// La deuda de otro es información sensible: la ve quien maneja la plata del
	// equipo, o el propio interesado.
	me, err := h.authz.requireMember(c, target.TeamID)
	if abortAuthz(c, err) {
		return
	}
	if me.ID != membershipID && me.Role != membership.RoleManager && me.Role != membership.RoleTreasurer {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}

	charges, err := h.charges.ListByMembership(c.Request.Context(), membershipID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list charges"})
		return
	}
	if charges == nil {
		charges = []*charge.Charge{}
	}
	c.JSON(http.StatusOK, charges)
}

// Split POST /teams/:id/charges
//
// Reparte el costo del lugar entre los jugadores convocados.
//
// El cuerpo solo dice QUÉ repartir, no cuánto: el monto por cabeza y quiénes
// pagan los deriva el servidor de la competencia y de la convocatoria. Antes
// esto aceptaba los montos del cliente y verificaba que sumaran un total que el
// propio cliente mandaba — o sea, no verificaba nada. Ahora un cliente
// manipulado no puede inventar deuda ni cobrarle a quien no juega.
func (h *ChargeHandler) Split(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager, membership.RoleTreasurer); abortAuthz(c, err) {
		return
	}

	var req struct {
		SourceType string `json:"source_type" binding:"required,oneof=match_cost"`
		SourceID   string `json:"source_id"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	comp, err := h.competitions.FindByID(c.Request.Context(), req.SourceID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if comp.VenueCost == nil || comp.VenueCost.Amount <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "this competition has no venue cost to split"})
		return
	}
	if comp.PlayersPerSide == nil || *comp.PlayersPerSide <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "this competition has no squad size configured"})
		return
	}
	// La cuota se guardó al crear la competencia. Si falta con costo y nómina
	// cargados, es una fila anterior a la migración 015 que no se completó, y
	// repartir con un número recalculado podría no coincidir con lo que ya se
	// le cobró a quienes aceptaron antes.
	if comp.PlayerShare == nil || *comp.PlayerShare <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "this competition has no per-player share on file"})
		return
	}

	// El partido es el que tiene la convocatoria. Repartir antes de que exista
	// significaría cobrarle a gente que todavía nadie citó.
	matches, err := h.matches.ListByCompetition(c.Request.Context(), comp.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the match"})
		return
	}
	var target *match.Match
	for _, m := range matches {
		if m.Status != match.StatusCancelled && m.Involves(teamID) {
			target = m
			break
		}
	}
	if target == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "there is no confirmed match for this competition yet"})
		return
	}

	callups, err := h.matches.ListCallups(c.Request.Context(), target.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the callups"})
		return
	}

	// Solo el propio plantel: la convocatoria del rival no se toca, y así un
	// id ajeno filtrado por cualquier vía queda afuera igual.
	members, err := h.memberships.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not verify the roster"})
		return
	}
	own := make(map[string]bool, len(members))
	for _, m := range members {
		own[m.MembershipID] = true
	}

	// Pagan los que van: confirmados y convocados. Quien avisó que no puede
	// queda afuera; cobrarle la cancha a alguien que no juega rompe la
	// confianza en la app.
	payers := make([]string, 0, len(callups))
	for _, cu := range callups {
		if !own[cu.MembershipID] {
			continue
		}
		if cu.Status == match.CallupConfirmed || cu.Status == match.CallupCalled {
			payers = append(payers, cu.MembershipID)
		}
	}
	if len(payers) == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "call up players before splitting the cost"})
		return
	}

	split := charge.SplitMatchCostWithShare(comp.VenueCost.Amount, *comp.PlayerShare, payers)

	charges, err := h.charges.CreateForSource(c.Request.Context(), charge.CreateInput{
		TeamID:   teamID,
		Source:   charge.Source{Type: charge.SourceMatchCost, ID: comp.ID},
		Currency: comp.VenueCost.Currency,
		Lines:    split.Lines,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create charges"})
		return
	}

	// El excedente queda anotado como fondo del equipo. Rehacer el reparto lo
	// recalcula: si un partido deja de sobrar, la entrada desaparece.
	if err := h.funds.Set(c.Request.Context(), teamID, funds.Source{
		Type: funds.SourceMatchCost,
		ID:   comp.ID,
	}, split.Surplus, comp.VenueCost.Currency); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not record team funds"})
		return
	}

	// Se avisa después de guardar y sin esperar el envío: el reparto ya está
	// hecho y que Expo tarde o falle no cambia nada de lo anterior.
	h.notifications.NotifyAsync(
		userIDsForMemberships(members, payers),
		"Nuevo cobro",
		"Se repartió el costo de la cancha. Toca para ver tu parte.",
		map[string]string{"type": "charge_created", "team_id": teamID},
	)

	c.JSON(http.StatusCreated, gin.H{
		"charges":    charges,
		"per_player": split.PerPlayer,
		"team_share": split.TeamShare,
		"surplus":    split.Surplus,
	})
}

// Funds GET /teams/:id/funds
//
// Los fondos del equipo: lo que cada reparto del costo de cancha dejó por
// encima de la mitad del lugar. Son acumulados, no de un mes, por eso viven
// fuera del resumen mensual de cuotas.
func (h *ChargeHandler) Funds(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	entries, err := h.funds.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list team funds"})
		return
	}
	if entries == nil {
		entries = []*funds.Entry{}
	}

	type item struct {
		Source    funds.Source `json:"source"`
		Name      string       `json:"name,omitempty"`
		Amount    int64        `json:"amount"`
		Currency  string       `json:"currency"`
		UpdatedAt time.Time    `json:"updated_at"`
	}
	items := make([]item, 0, len(entries))
	var total int64
	var currency string
	for _, e := range entries {
		name := ""
		if e.Source.Type == funds.SourceMatchCost {
			if comp, err := h.competitions.FindByID(c.Request.Context(), e.Source.ID); err == nil {
				name = comp.Name
			}
		}
		items = append(items, item{
			Source:    e.Source,
			Name:      name,
			Amount:    e.Amount,
			Currency:  e.Currency,
			UpdatedAt: e.UpdatedAt,
		})
		total += e.Amount
		if currency == "" {
			currency = e.Currency
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    total,
		"currency": currency,
		"entries":  items,
	})
}

// SubmitReceipt POST /charges/:chargeId/receipt
//
// Lo sube el propio deudor: nadie paga por otro. Y con eso el cargo queda
// pagado, sin pasar por la revisión de nadie —la app confía en quien declara
// haber transferido—, así que este es el único endpoint que cierra un cobro.
func (h *ChargeHandler) SubmitReceipt(c *gin.Context) {
	ch, me, ok := h.loadCharge(c)
	if !ok {
		return
	}
	if ch.MembershipID != me.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "this charge belongs to another member"})
		return
	}

	var req struct {
		ReceiptURL string `json:"receipt_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.charges.SubmitReceipt(c.Request.Context(), ch.ID, req.ReceiptURL, time.Now())
	if errors.Is(err, charge.ErrNotPayable) {
		c.JSON(http.StatusConflict, gin.H{"error": charge.ErrNotPayable.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not submit the receipt"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Confirm POST /charges/:chargeId/confirm
//
// Lo confirma quien maneja la plata, nunca el propio deudor.
//
// LEGADO: la app ya no lo llama. Desde que el comprobante da el cargo por
// pagado no queda nada en 'submitted', que es lo único sobre lo que este
// endpoint opera, así que hoy responde 409 siempre. No se borró porque un
// rollback del backend vuelve a producir ese estado y hay que poder cerrarlo.
func (h *ChargeHandler) Confirm(c *gin.Context) {
	ch, me, ok := h.loadCharge(c)
	if !ok {
		return
	}
	if me.Role != membership.RoleManager && me.Role != membership.RoleTreasurer {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}
	// Confirmarse el pago a uno mismo elimina el control de dos ojos que
	// justifica que exista el estado 'submitted'.
	if ch.MembershipID == me.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "someone else has to confirm your own payment"})
		return
	}

	userID, _ := currentUserID(c)
	updated, err := h.charges.Confirm(c.Request.Context(), ch.ID, userID, time.Now())
	if errors.Is(err, charge.ErrNotSubmitted) {
		c.JSON(http.StatusConflict, gin.H{"error": charge.ErrNotSubmitted.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not confirm the payment"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// RejectReceipt POST /charges/:chargeId/reject
func (h *ChargeHandler) RejectReceipt(c *gin.Context) {
	ch, me, ok := h.loadCharge(c)
	if !ok {
		return
	}
	if me.Role != membership.RoleManager && me.Role != membership.RoleTreasurer {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}

	updated, err := h.charges.RejectReceipt(c.Request.Context(), ch.ID)
	if errors.Is(err, charge.ErrNotSubmitted) {
		c.JSON(http.StatusConflict, gin.H{"error": charge.ErrNotSubmitted.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not reject the receipt"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// loadCharge trae el cargo y la membresía de quien pide, ya validada contra el
// equipo dueño del cargo.
func (h *ChargeHandler) loadCharge(c *gin.Context) (*charge.Charge, *membership.Membership, bool) {
	ch, err := h.charges.FindByID(c.Request.Context(), c.Param("chargeId"))
	if errors.Is(err, charge.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "charge not found"})
		return nil, nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}

	me, err := h.authz.requireMember(c, ch.TeamID)
	if abortAuthz(c, err) {
		return nil, nil, false
	}
	return ch, me, true
}
