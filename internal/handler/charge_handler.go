package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

type ChargeHandler struct {
	charges       charge.Repository
	competitions  competition.Repository
	matches       match.Repository
	memberships   membership.Repository
	funds         funds.Repository
	notifications *notify.Service
	authz         teamAuthorizer
}

func NewChargeHandler(
	charges charge.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
	funds funds.Repository,
	notifications *notify.Service,
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

// ListByCompetition GET /competitions/:competitionId/charges
//
// Devuelve solo los cargos de los equipos de quien consulta. En un amistoso los
// dos planteles cuelgan de la misma fuente (`match_cost` + competencia), y antes
// esto validaba contra el equipo del primer cargo y devolvía todo: si un rival
// confirmaba primero, el plantel propio entero recibía 403 y nadie podía pagar
// su parte; si no, el manager veía la deuda del otro equipo sumada a la suya.
func (h *ChargeHandler) ListByCompetition(c *gin.Context) {
	charges, err := h.charges.ListBySource(c.Request.Context(), charge.Source{
		Type: charge.SourceMatchCost,
		ID:   c.Param("competitionId"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list charges"})
		return
	}

	// Se pregunta una vez por equipo y no por cargo: son a lo más dos, y en un
	// partido interno uno solo.
	allowed := make(map[string]bool, 2)
	var denied error
	own := []*charge.Charge{}
	for _, ch := range charges {
		ok, seen := allowed[ch.TeamID]
		if !seen {
			_, err := h.authz.requireMember(c, ch.TeamID)
			ok = err == nil
			allowed[ch.TeamID] = ok
			if err != nil {
				denied = err
			}
		}
		if ok {
			own = append(own, ch)
		}
	}
	if len(charges) > 0 && len(own) == 0 && abortAuthz(c, denied) {
		return
	}
	c.JSON(http.StatusOK, own)
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
	// equipo, o el propio interesado. Los invitados entran por lo segundo —el
	// chequeo de abajo los deja solo con lo suyo, porque no tienen rol.
	me, err := h.authz.requireMembership(c, target.TeamID)
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

// reminderZone es el reloj con el que se cuenta "uno por día". Es el del
// equipo y no UTC: en UTC el día cambia a las 21:00 de Chile, y un manager que
// recuerda a las 20:00 y a las 22:00 mandaría dos avisos la misma noche.
const reminderZone = "America/Santiago"

/*
RemindPending POST /teams/:id/charge-reminders

Les recuerda a los confirmados del propio equipo que todavía deben su parte de
la cancha. Reemplaza al viejo "repartir", que dejó de tener sentido el día que
el cobro empezó a nacer al confirmar (`ensureMatchCharge`).

La regla que sostiene es una sola: **un cobro existe si y solo si confirmaste**.
El reparto la rompía —cobraba también a los citados que nunca contestaron— y
además borraba los pendientes del rival, porque los dos equipos comparten el
mismo origen y el DELETE no miraba el equipo. Esto no borra nada y no le crea
deuda a quien no confirmó.

Lo que sí crea: el cobro de un confirmado que por algún motivo no lo tiene.
`ensureMatchCharge` falla en silencio a propósito —no puede tumbar la respuesta
a la citación— y este es el lugar natural para completarlo, porque es justo
cuando el manager mira quién debe.

El aviso va a los que deben (pendiente), cada uno a SU cobro, y con clave
diaria: a lo más uno por persona, por partido y por día. Tocar el botón dos
veces no manda dos pushes, y la respuesta dice a cuántos les llegó de verdad.

Lee el cuerpo viejo del reparto (`source_type` + `source_id`) porque la 1.0.x
todavía llama a `POST /teams/:id/charges` con él.
*/
func (h *ChargeHandler) RemindPending(c *gin.Context) {
	ctx := c.Request.Context()
	teamID := c.Param("id")
	me, err := h.authz.requireRole(c, teamID, membership.RoleManager, membership.RoleTreasurer)
	if abortAuthz(c, err) {
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

	comp, err := h.competitions.FindByID(ctx, req.SourceID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	// La cuota se guardó al crear la competencia. Sin ella no hay cobro que
	// recordar: nadie le puso precio al lugar.
	if comp.VenueCost == nil || comp.PlayerShare == nil || *comp.PlayerShare <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "this competition has no per-player share on file"})
		return
	}

	// Un partido cancelado no se cobra: sus cobros ya están anulados y lo que
	// queda es devolver, no recordar.
	matches, err := h.matches.ListByCompetition(ctx, comp.ID)
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
		c.JSON(http.StatusConflict, gin.H{"error": "there is no confirmed match for this competition"})
		return
	}

	callups, err := h.matches.ListCallups(ctx, target.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the callups"})
		return
	}

	// Solo el propio equipo, invitados incluidos: el parche confirmado paga
	// igual que cualquiera. La convocatoria del rival no es asunto nuestro.
	members, err := h.memberships.ListByTeam(ctx, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not verify the roster"})
		return
	}
	userByMembership := make(map[string]string, len(members))
	for _, m := range members {
		userByMembership[m.MembershipID] = m.UserID
	}

	source := charge.Source{Type: charge.SourceMatchCost, ID: comp.ID}
	pending := make([]notify.Recipient, 0, len(callups))
	for _, cu := range callups {
		userID, own := userByMembership[cu.MembershipID]
		if !own || cu.Status != match.CallupConfirmed {
			continue
		}
		ch, err := h.charges.EnsureForMembership(ctx, charge.EnsureInput{
			TeamID:       teamID,
			MembershipID: cu.MembershipID,
			Source:       source,
			Amount:       *comp.PlayerShare,
			Currency:     comp.VenueCost.Currency,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the charges"})
			return
		}
		// Quien maneja la plata y además juega no se manda el aviso a sí mismo:
		// es quien está tocando el botón.
		if ch.Status == charge.StatusPending && userID != me.UserID {
			pending = append(pending, notify.Recipient{UserID: userID, EntityID: ch.ID})
		}
	}

	notified, err := h.notifications.EmitNow(ctx, notify.Event{
		TeamID:     teamID,
		Type:       notification.TypeChargeReminder,
		Title:      "Recordatorio de pago",
		Body:       reminderBody(comp),
		Recipients: pending,
		DedupeKey:  notification.DedupeKey(string(notification.TypeChargeReminder), comp.ID, reminderDay(time.Now())),
		OnConflict: notification.ConflictSkip,
	})
	if err != nil {
		// Los cobros quedaron en orden; lo que falló es el aviso. Se reporta
		// porque la pantalla le va a decir al manager a cuántos les llegó.
		slog.Error("could not record the payment reminders",
			"error", err, "competition_id", comp.ID, "team_id", teamID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not send the reminders"})
		return
	}

	// Los cobros vuelven filtrados por equipo: el origen es compartido con el
	// rival, y la 1.0.x pinta lo que reciba acá.
	all, err := h.charges.ListBySource(ctx, source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list charges"})
		return
	}
	own := make([]*charge.Charge, 0, len(all))
	for _, ch := range all {
		if ch.TeamID == teamID {
			own = append(own, ch)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		// A cuántos les llegó un aviso nuevo con este toque.
		"notified": notified,
		// Cuántos deben, avisados hoy o no. Con `notified` en cero y esto en
		// más, la pantalla sabe que ya se les recordó hoy.
		"pending": len(pending),
		"charges": own,
	})
}

// reminderBody nombra el partido cuando hay nombre: un jugador puede deber dos
// canchas a la vez, y "tu parte de la cancha" a secas no dice de cuál.
func reminderBody(comp *competition.Competition) string {
	if comp.Name == "" {
		return "Todavía falta tu parte de la cancha. Toca para pagarla."
	}
	return "Todavía falta tu parte de la cancha de " + comp.Name + ". Toca para pagarla."
}

// reminderDay es el día calendario de Chile, que es lo que cuenta para "uno por
// día". Sin la base de zonas cae a UTC: un día corrido es mejor que no avisar.
func reminderDay(now time.Time) string {
	if location, err := time.LoadLocation(reminderZone); err == nil {
		now = now.In(location)
	}
	return now.Format("2006-01-02")
}

// Funds GET /teams/:id/funds
//
// Los fondos del equipo: lo que cada partido jugado dejó por encima de lo que al
// equipo le tocaba poner del lugar. Son acumulados, no de un mes, por eso viven
// fuera del resumen mensual de cuotas. Se derivan de los cobros: ver
// `funds.ListByTeam`.
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
		items = append(items, item{
			Source:    e.Source,
			Name:      e.SourceName,
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

	h.notifyPaymentReceived(c.Request.Context(), updated, me)

	c.JSON(http.StatusOK, updated)
}

/*
notifyPaymentReceived le avisa al que maneja la plata que alguien pagó.

El título es "Pago recibido" y no "comprobante enviado" porque eso es lo que
pasó: mandar el comprobante deja el cargo en `paid` en el acto y nadie lo
revisa. Un aviso que dijera "revisa este comprobante" prometería una acción que
la app no tiene.

Y lleva al PARTIDO, no al cobro. No existe `GET /charges/:id` —la pantalla del
cargo resuelve buscando dentro de la lista del propio jugador— así que un manager
que llegara ahí vería "este cobro ya no está". Su vista sobre los cobros es la
sección del partido, que además le muestra todos los cobros y no solo el que
acaba de entrar.

Va al manager y al tesorero: el tesorero existe justamente para que el manager no
sea el único que mira la plata.
*/
func (h *ChargeHandler) notifyPaymentReceived(
	ctx context.Context, ch *charge.Charge, payer *membership.Membership,
) {
	if !h.notifications.Enabled() || ch.Source.Type != charge.SourceMatchCost {
		return
	}
	matches, err := h.matches.ListByCompetition(ctx, ch.Source.ID)
	if err != nil {
		slog.Error("could not read the match to announce the payment",
			"error", err, "competition_id", ch.Source.ID)
		return
	}
	var target *match.Match
	for _, m := range matches {
		if m.Status != match.StatusCancelled && m.Involves(ch.TeamID) {
			target = m
			break
		}
	}
	if target == nil {
		return
	}

	h.notifications.EmitAsync(notify.Event{
		TeamID:     ch.TeamID,
		Type:       notification.TypePaymentReceived,
		EntityID:   target.ID,
		Title:      "Pago recibido",
		Body:       personName(ctx, h.memberships, payer.ID) + " pagó su parte de la cancha.",
		Recipients: notify.To(moneyHandlerIDs(ctx, h.memberships, ch.TeamID, payer.UserID)...),
	})
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

// Waive POST /charges/:chargeId/waive
//
// Cierra el cobro con plata que entró **fuera** de la app: se pagó en efectivo,
// o lo cubre de su bolsillo quien maneja la plata del equipo. Lo hace ese, nunca
// el deudor —si no, cualquiera cierra su propia cuota.
//
// No es condonar, aunque el estado se siga llamando `waived`: la cancha ya se
// pagó, así que el monto sale de algún lado, y la app lo cuenta como cobrado
// (ver `isCollected` en el móvil). `confirmed_by` guarda quién lo cerró, que es
// lo que distingue esto de un pago declarado por el propio deudor.
//
// Es la única salida que tiene un pendiente que no va a entrar por la app. Con
// los invitados de un partido deja de ser un caso raro: al que vino un sábado y
// no volvió no hay cuota mensual donde arrastrarle la deuda, y sin esto se
// quedaba en la lista del manager para siempre.
func (h *ChargeHandler) Waive(c *gin.Context) {
	ch, me, ok := h.loadCharge(c)
	if !ok {
		return
	}
	if me.Role != membership.RoleManager && me.Role != membership.RoleTreasurer {
		c.JSON(http.StatusForbidden, gin.H{"error": ErrInsufficient.Error()})
		return
	}

	updated, err := h.charges.Waive(c.Request.Context(), ch.ID, me.UserID, time.Now())
	if errors.Is(err, charge.ErrAlreadySettled) {
		c.JSON(http.StatusConflict, gin.H{"error": charge.ErrAlreadySettled.Error()})
		return
	}
	if errors.Is(err, charge.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": charge.ErrNotFound.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not waive the charge"})
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

	// Incluye a los invitados: el parche paga su cuota de cancha por la app
	// como cualquiera, así que tiene que poder llegar a su propio cargo. Quién
	// puede hacer qué con él lo deciden los handlers —SubmitReceipt exige que
	// el cargo sea suyo, y confirmar o condonar exige rol, que un invitado no
	// tiene— así que este guard no necesita saber de partidos.
	me, err := h.authz.requireMembership(c, ch.TeamID)
	if abortAuthz(c, err) {
		return nil, nil, false
	}
	return ch, me, true
}
