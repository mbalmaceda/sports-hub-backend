package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

// La deuda del amistoso: el retado (away) le debe al organizador (home).
func pendingSettlement() *settlement.Settlement {
	return &settlement.Settlement{
		ID:         "st-1",
		Source:     settlement.Source{Type: settlement.SourceMatchCost, ID: "comp-1"},
		FromTeamID: awayTeam,
		ToTeamID:   homeTeam,
		Amount:     14000,
		Currency:   "CLP",
		Status:     settlement.StatusPending,
	}
}

type settlementMocks struct {
	settlements  *testutil.MockSettlementRepo
	competitions *testutil.MockCompetitionRepo
	matches      *testutil.MockMatchRepo
	memberships  *testutil.MockMembershipRepo
	teams        *testutil.MockTeamRepo
}

func newSettlementHandler() (*handler.SettlementHandler, settlementMocks) {
	m := settlementMocks{
		settlements:  &testutil.MockSettlementRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		matches:      &testutil.MockMatchRepo{},
		memberships:  &testutil.MockMembershipRepo{},
		teams:        &testutil.MockTeamRepo{},
	}
	h := handler.NewSettlementHandler(m.settlements, m.competitions, m.matches, m.memberships, m.teams)
	return h, m
}

func settlementRequest(userID, method, path string) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, userID)
	c.Params = gin.Params{{Key: "settlementId", Value: "st-1"}}
	c.Request = httptest.NewRequest(method, path, nil)
	return w, c
}

// El manager del equipo retado declara la transferencia y la deuda queda saldada.
func TestPaySettlement_DebtorManagerPays(t *testing.T) {
	h, m := newSettlementHandler()

	paid := pendingSettlement()
	paid.Status = settlement.StatusPaid
	m.settlements.On("FindByID", mock.Anything, "st-1").Return(pendingSettlement(), nil)
	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	m.settlements.On("MarkPaid", mock.Anything, "st-1", "user-away", mock.Anything).Return(paid, nil)

	w, c := settlementRequest("user-away", http.MethodPost, "/settlements/st-1/pay")
	h.Pay(c)

	assert.Equal(t, http.StatusOK, w.Code)
	m.settlements.AssertExpectations(t)
}

/*
El que cobra no puede dar por pagada su propia acreencia.

Es la guarda que sostiene todo el modelo: acá nadie verifica la transferencia,
se le cree al que dice haberla hecho. Si el acreedor pudiera tocar el mismo
botón, "pagado" dejaría de querer decir que alguien transfirió.
*/
func TestPaySettlement_CreditorCannotPay(t *testing.T) {
	h, m := newSettlementHandler()

	m.settlements.On("FindByID", mock.Anything, "st-1").Return(pendingSettlement(), nil)
	// El manager del organizador no tiene membresía en el equipo deudor.
	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", awayTeam).
		Return(nil, membership.ErrNotFound)

	w, c := settlementRequest("user-home", http.MethodPost, "/settlements/st-1/pay")
	h.Pay(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	m.settlements.AssertNotCalled(t, "MarkPaid", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador del equipo deudor tampoco: es plata del equipo.
func TestPaySettlement_PlayerCannotPay(t *testing.T) {
	h, m := newSettlementHandler()

	m.settlements.On("FindByID", mock.Anything, "st-1").Return(pendingSettlement(), nil)
	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-player", awayTeam).
		Return(&membership.Membership{
			ID: "m-1", UserID: "user-player", TeamID: awayTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w, c := settlementRequest("user-player", http.MethodPost, "/settlements/st-1/pay")
	h.Pay(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	m.settlements.AssertNotCalled(t, "MarkPaid", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Declarar dos veces la misma transferencia no pisa al autor ni la fecha de la
// primera: el UPDATE solo toca pendientes y el handler traduce eso a un 409.
func TestPaySettlement_AlreadyPaidIsRejected(t *testing.T) {
	h, m := newSettlementHandler()

	m.settlements.On("FindByID", mock.Anything, "st-1").Return(pendingSettlement(), nil)
	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	m.settlements.On("MarkPaid", mock.Anything, "st-1", "user-away", mock.Anything).
		Return(nil, settlement.ErrAlreadyPaid)

	w, c := settlementRequest("user-away", http.MethodPost, "/settlements/st-1/pay")
	h.Pay(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// El deudor lee los datos bancarios del organizador, que es a quien le tiene que
// transferir. Es la única puerta: `GET /teams/:id/bank-account` exige ser de ese
// equipo, y el rival justamente no lo es.
func TestSettlementBankAccount_ReturnsThePayeeAccount(t *testing.T) {
	h, m := newSettlementHandler()

	m.settlements.On("FindByID", mock.Anything, "st-1").Return(pendingSettlement(), nil)
	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	// La cuenta que se pide es la del acreedor, no la del que pregunta.
	m.teams.On("GetBankAccount", mock.Anything, homeTeam).
		Return(&team.BankAccount{TeamID: homeTeam, BankName: "Banco Estado"}, nil)

	w, c := settlementRequest("user-away", http.MethodGet, "/settlements/st-1/bank-account")
	h.GetPayeeBankAccount(c)

	assert.Equal(t, http.StatusOK, w.Code)
	m.teams.AssertExpectations(t)
}

// Las liquidaciones del equipo son plata del equipo: no las lee todo el plantel.
func TestListSettlements_PlayerCannotRead(t *testing.T) {
	h, m := newSettlementHandler()

	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-player", awayTeam).
		Return(&membership.Membership{
			ID: "m-1", UserID: "user-player", TeamID: awayTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: awayTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+awayTeam+"/settlements", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	m.settlements.AssertNotCalled(t, "ListByTeam", mock.Anything, mock.Anything)
}

// El tesorero sí: es justamente quien maneja la plata.
func TestListSettlements_TreasurerCanRead(t *testing.T) {
	h, m := newSettlementHandler()

	m.memberships.On("FindByUserAndTeam", mock.Anything, "user-treasurer", awayTeam).
		Return(&membership.Membership{
			ID: "m-2", UserID: "user-treasurer", TeamID: awayTeam,
			Role: membership.RoleTreasurer, Status: membership.StatusActive,
		}, nil)
	m.settlements.On("ListByTeam", mock.Anything, awayTeam).
		Return([]*settlement.Settlement{pendingSettlement()}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-treasurer")
	c.Params = gin.Params{{Key: "id", Value: awayTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+awayTeam+"/settlements", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusOK, w.Code)
	m.settlements.AssertExpectations(t)
}
