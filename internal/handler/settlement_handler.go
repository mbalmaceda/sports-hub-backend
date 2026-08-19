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
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

/*
SettlementHandler: la mitad de la cancha que un equipo le debe al otro.

El organizador reserva y paga el lugar entero. Cada equipo le cobra a sus
jugadores su parte —eso ya funcionaba— pero la mitad del rival se quedaba en la
cuenta del rival. Acá vive el tramo que faltaba: una transferencia entre
managers que cierra el partido en cero para los dos.
*/
type SettlementHandler struct {
	settlements  settlement.Repository
	competitions competition.Repository
	teams        team.Repository
	authz        teamAuthorizer
	access       competitionAccess
}

func NewSettlementHandler(
	settlements settlement.Repository,
	competitions competition.Repository,
	matches match.Repository,
	memberships membership.Repository,
	teams team.Repository,
) *SettlementHandler {
	authz := teamAuthorizer{memberships: memberships}
	return &SettlementHandler{
		settlements:  settlements,
		competitions: competitions,
		teams:        teams,
		authz:        authz,
		access: competitionAccess{
			authz:        authz,
			competitions: competitions,
			matches:      matches,
		},
	}
}

// ListByTeam GET /teams/:id/settlements
//
// Las dos direcciones en una: lo que el equipo debe y lo que le deben. Es plata
// del equipo, así que la lee quien la maneja y no todo el plantel.
func (h *SettlementHandler) ListByTeam(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(
		c, teamID, membership.RoleManager, membership.RoleTreasurer,
	); abortAuthz(c, err) {
		return
	}

	items, err := h.settlements.ListByTeam(c.Request.Context(), teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list settlements"})
		return
	}
	if items == nil {
		items = []*settlement.Settlement{}
	}
	c.JSON(http.StatusOK, items)
}

// GetByCompetition GET /competitions/:competitionId/settlement
//
// La deuda de un partido, para el balance de las dos pantallas. Entra por el
// mismo guard que la competencia —lo lee quien la juega— porque no dice nada
// que el jugador no sepa ya: es la mitad de un costo que la app le muestra
// entero desde que se creó el amistoso.
//
// 404 cuando no hay es un caso normal y no un error: una cancha gratis o un
// partido interno no generan ninguna.
func (h *SettlementHandler) GetByCompetition(c *gin.Context) {
	comp, ok := h.access.requireByID(c, c.Param("competitionId"))
	if !ok {
		return
	}

	s, err := h.settlements.FindBySource(c.Request.Context(), settlement.Source{
		Type: settlement.SourceMatchCost, ID: comp.ID,
	})
	if errors.Is(err, settlement.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "this competition has no settlement between teams"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, s)
}

// GetPayeeBankAccount GET /settlements/:settlementId/bank-account
//
// A qué cuenta transferir. Es la única forma de que el deudor lea los datos
// bancarios del organizador: `GET /teams/:id/bank-account` exige ser de ese
// equipo, y el rival justamente no lo es.
//
// Va por su propio endpoint y no adentro de la liquidación a propósito: la
// liquidación se lista en el inicio, y datos bancarios en una respuesta de
// lista es información sensible viajando cada vez que alguien abre la app. Acá
// sale una sola vez, cuando alguien va a pagar de verdad.
func (h *SettlementHandler) GetPayeeBankAccount(c *gin.Context) {
	s, ok := h.loadForDebtor(c)
	if !ok {
		return
	}

	acc, err := h.teams.GetBankAccount(c.Request.Context(), s.ToTeamID)
	if errors.Is(err, team.ErrBankAccountNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "team has no bank account on file"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, acc)
}

// Pay POST /settlements/:settlementId/pay
//
// El que transfiere declara y se le cree, igual que el comprobante de un cobro.
// Entre dos managers que acaban de jugar juntos, un estado intermedio esperando
// que el otro confirme costaba más de lo que cuidaba.
//
// No pide comprobante: hoy la app no tiene dónde guardar la imagen —lo que
// viaja en los cobros es un `file://` que solo resuelve en el teléfono que lo
// subió— y pedir uno que se descarta sería repetir a sabiendas lo que ya es
// deuda técnica conocida.
func (h *SettlementHandler) Pay(c *gin.Context) {
	s, ok := h.loadForDebtor(c)
	if !ok {
		return
	}

	userID, _ := currentUserID(c)
	paid, err := h.settlements.MarkPaid(c.Request.Context(), s.ID, userID, time.Now())
	if errors.Is(err, settlement.ErrAlreadyPaid) {
		c.JSON(http.StatusConflict, gin.H{"error": settlement.ErrAlreadyPaid.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not record the transfer"})
		return
	}
	c.JSON(http.StatusOK, paid)
}

// loadForDebtor carga la liquidación y exige manejar la plata del equipo que
// debe. El que cobra no entra: no tiene nada que hacer acá, y darle la acción
// de pagar le dejaría cerrar una deuda que nadie transfirió.
func (h *SettlementHandler) loadForDebtor(c *gin.Context) (*settlement.Settlement, bool) {
	s, err := h.settlements.FindByID(c.Request.Context(), c.Param("settlementId"))
	if errors.Is(err, settlement.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "settlement not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}

	if _, err := h.authz.requireRole(
		c, s.FromTeamID, membership.RoleManager, membership.RoleTreasurer,
	); abortAuthz(c, err) {
		return nil, false
	}
	return s, true
}

/*
ensureVenueSettlement anota lo que el equipo retado le debe al organizador.

Vive suelta y no como método porque la llama el amistoso al aceptarse, que es el
único momento en que existe el compromiso: antes hay un desafío que todavía
puede quedar sin respuesta, y después ya nadie vuelve a pasar por ahí.

No devuelve error a propósito, igual que `ensureMatchCharge`: el amistoso ya
quedó aceptado y el partido creado, que es lo que los dos equipos vinieron a
hacer. Fallar acá y devolver un 500 dejaría a los managers creyendo que el
partido no se acordó, cuando en Postgres sí está. Lo que se pierde es el aviso
de la deuda, que se puede volver a generar; lo que se salvaría no existe.

El monto es la mitad del lugar, calculada con el mismo `charge.TeamShare` que
usa el reparto: si los dos redondearan por su cuenta, quedaría un peso colgado
que nadie sabría de quién es.
*/
func ensureVenueSettlement(
	ctx context.Context,
	settlements settlement.Repository,
	comp *competition.Competition,
	organizerTeamID, rivalTeamID string,
) {
	// Cancha gratis no genera deuda. Es un caso normal —muchos amistosos se
	// juegan en una cancha pública— y no algo que haya que anotar en cero.
	if comp.VenueCost == nil || comp.VenueCost.Amount <= 0 {
		return
	}
	// Un equipo no se debe a sí mismo. El partido interno no llega acá —nace
	// por su propia puerta, sin desafío— pero el CHECK de la tabla lo rechaza
	// igual, y esto evita el error de base de datos en el log.
	if organizerTeamID == rivalTeamID {
		return
	}

	if _, err := settlements.Create(ctx, &settlement.Settlement{
		Source:     settlement.Source{Type: settlement.SourceMatchCost, ID: comp.ID},
		FromTeamID: rivalTeamID,
		ToTeamID:   organizerTeamID,
		Amount:     charge.TeamShare(comp.VenueCost.Amount),
		Currency:   comp.VenueCost.Currency,
	}); err != nil {
		slog.Error("could not record what the rival owes for the venue",
			"error", err, "competition_id", comp.ID, "rival_team_id", rivalTeamID)
	}
}
