package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/expense"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

type expenseDeps struct {
	expenses     *testutil.MockExpenseRepo
	competitions *testutil.MockCompetitionRepo
	members      *testutil.MockMembershipRepo
}

func newExpenseHandler() (*handler.ExpenseHandler, expenseDeps) {
	d := expenseDeps{
		expenses:     &testutil.MockExpenseRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		members:      &testutil.MockMembershipRepo{},
	}
	return handler.NewExpenseHandler(d.expenses, d.competitions, d.members), d
}

func createExpenseRequest(c *gin.Context, body string) {
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/expenses",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
}

func playerOf(teamID, userID string) *membership.Membership {
	return &membership.Membership{
		ID: "m-" + userID, UserID: userID, TeamID: teamID,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}
}

// Un gasto que no cuelga de ningún partido es válido: las pelotas no son de un
// amistoso puntual.
func TestCreateExpense_WithoutSource(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)

	d.expenses.On("Create", mock.Anything, mock.MatchedBy(func(in expense.CreateInput) bool {
		return in.Source == nil && in.Amount == 15000 && in.TeamID == homeTeam &&
			in.RecordedBy == "user-mgr" && in.Category == "equipment"
	})).Return(&expense.Expense{ID: "exp-1", TeamID: homeTeam, Amount: 15000}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":15000,"currency":"CLP","category":"equipment",
		"description":"Pelotas","expense_date":"2026-08-09"}`)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.expenses.AssertExpectations(t)
}

// Con origen, el gasto entra al balance de ese partido.
func TestCreateExpense_WithMatchSource(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").
		Return(&competition.Competition{ID: "comp-1", OrganizerTeamID: homeTeam}, nil)

	d.expenses.On("Create", mock.Anything, mock.MatchedBy(func(in expense.CreateInput) bool {
		return in.Source != nil &&
			in.Source.Type == expense.SourceMatchCost &&
			in.Source.ID == "comp-1"
	})).Return(&expense.Expense{ID: "exp-2"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":20000,"currency":"CLP","category":"referee",
		"source_type":"match_cost","source_id":"comp-1"}`)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.expenses.AssertExpectations(t)
}

// Colgarle un gasto al partido de otro club le ensuciaría el balance desde
// afuera.
func TestCreateExpense_RejectsForeignCompetition(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-otro").
		Return(&competition.Competition{ID: "comp-otro", OrganizerTeamID: awayTeam}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":20000,"currency":"CLP","category":"referee",
		"source_type":"match_cost","source_id":"comp-otro"}`)

	h.Create(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.expenses.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Media referencia no apunta a nada.
func TestCreateExpense_RejectsHalfSource(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":1000,"currency":"CLP","category":"x","source_type":"match_cost"}`)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	d.expenses.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Un jugador no mueve la plata del equipo.
func TestCreateExpense_PlayerCannotRecord(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	createExpenseRequest(c, `{"amount":1000,"currency":"CLP","category":"x"}`)

	h.Create(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.expenses.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCreateExpense_RejectsNonPositiveAmount(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":0,"currency":"CLP","category":"x"}`)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	d.expenses.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Sin fecha, el gasto es de hoy: el caso corriente es anotar lo recién pagado.
func TestCreateExpense_DefaultsToToday(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)

	today := time.Now().UTC().Format("2006-01-02")
	d.expenses.On("Create", mock.Anything, mock.MatchedBy(func(in expense.CreateInput) bool {
		return in.ExpenseDate.Format("2006-01-02") == today
	})).Return(&expense.Expense{ID: "exp-3"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	createExpenseRequest(c, `{"amount":5000,"currency":"CLP","category":"x"}`)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.expenses.AssertExpectations(t)
}

// Los gastos del mes los ve cualquier miembro: es información del grupo.
func TestListExpenses_AnyMemberCanRead(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)
	d.expenses.On("ListByTeamAndPeriod", mock.Anything, homeTeam, 2026, 8).
		Return([]*expense.Expense{{ID: "exp-1", Amount: 15000}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/expenses?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, float64(15000), body[0]["amount"])
}

// Sin gastos devuelve lista vacía, no null: el cliente itera sin chequear.
func TestListExpenses_EmptyIsArray(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)
	d.expenses.On("ListByTeamAndPeriod", mock.Anything, homeTeam, 2026, 8).
		Return(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/expenses?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

func TestListExpenses_RejectsInvalidMonth(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/expenses?year=2026&month=13", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Un extraño no ve los gastos del equipo.
func TestListExpenses_NonMemberIsRejected(t *testing.T) {
	h, d := newExpenseHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-ajeno", homeTeam).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-ajeno")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/expenses?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.expenses.AssertNotCalled(t, "ListByTeamAndPeriod",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteExpense_RequiresRoleOnOwningTeam(t *testing.T) {
	h, d := newExpenseHandler()

	d.expenses.On("GetByID", mock.Anything, "exp-1").
		Return(&expense.Expense{ID: "exp-1", TeamID: homeTeam}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	c.Params = gin.Params{{Key: "expenseId", Value: "exp-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/expenses/exp-1", nil)

	h.Delete(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.expenses.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
}

func TestDeleteExpense_ManagerCanDelete(t *testing.T) {
	h, d := newExpenseHandler()

	d.expenses.On("GetByID", mock.Anything, "exp-1").
		Return(&expense.Expense{ID: "exp-1", TeamID: homeTeam}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.expenses.On("Delete", mock.Anything, "exp-1").Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	c.Params = gin.Params{{Key: "expenseId", Value: "exp-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/expenses/exp-1", nil)

	h.Delete(c)

	// Un 204 no escribe cuerpo, así que fuera del router el header no se vuelca
	// solo. Mismo empujón que en user_handler_test.
	c.Writer.WriteHeaderNow()

	assert.Equal(t, http.StatusNoContent, w.Code)
	d.expenses.AssertExpectations(t)
}

// Los gastos de un partido salen por su competencia, igual que los cobros.
func TestListExpensesByCompetition(t *testing.T) {
	h, d := newExpenseHandler()

	d.competitions.On("FindByID", mock.Anything, "comp-1").
		Return(&competition.Competition{ID: "comp-1", OrganizerTeamID: homeTeam}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-jug", homeTeam).
		Return(playerOf(homeTeam, "user-jug"), nil)
	d.expenses.On("ListBySource", mock.Anything,
		expense.Source{Type: expense.SourceMatchCost, ID: "comp-1"}).
		Return([]*expense.Expense{{ID: "exp-1", Amount: 20000}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-jug")
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/competitions/comp-1/expenses", nil)

	h.ListByCompetition(c)

	assert.Equal(t, http.StatusOK, w.Code)
	d.expenses.AssertExpectations(t)
}
