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

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

// newFeeHandler deja al usuario "tesorero" con ese rol en cualquier equipo:
// generar y cobrar cuotas es de quien maneja la plata. Los tests de rechazo
// arman su propio repo.
func newFeeHandler(feeRepo *testutil.MockFeeRepo, memberRepo *testutil.MockMembershipRepo, teamRepo *testutil.MockTeamRepo) *handler.FeeHandler {
	memberRepo.On("FindByUserAndTeam", mock.Anything, "tesorero", mock.Anything).
		Return(treasurerOf("", "tesorero", "m-tesorero"), nil)
	return handler.NewFeeHandler(feeRepo, memberRepo, teamRepo)
}

func TestGenerateFees_Success(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{
		ID: teamID, Name: "Deportivo Norte",
		FeeAmount: 10000, FeeDueDay: 5, Currency: "CLP",
	}
	members := []*membership.TeamMember{
		{MembershipID: "m-1", UserID: "u-1", TeamID: teamID, Status: "active", PlaysAsPlayer: true},
		{MembershipID: "m-2", UserID: "u-2", TeamID: teamID, Status: "active", PlaysAsPlayer: true},
		{MembershipID: "m-3", UserID: "u-3", TeamID: teamID, Status: "inactive", PlaysAsPlayer: true},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)
	// Solo los 2 activos deben llegar a BulkCreate
	feeRepo.On("BulkCreate", mock.Anything, mock.MatchedBy(func(obs []*fee.Obligation) bool {
		return len(obs) == 2
	})).Return(2, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+teamID+"/generate-fees",
		strings.NewReader(`{"period_year":2026,"period_month":7}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(2), body["created"])
	assert.Equal(t, float64(0), body["skipped"])

	feeRepo.AssertExpectations(t)
	memberRepo.AssertExpectations(t)
}

// La cuota la paga quien juega. El que solo dirige no la debe, y el manager que
// además juega sí: por eso el filtro mira plays_as_player y no el rol.
func TestGenerateFees_OnlyForMembersWhoPlay(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{ID: teamID, Name: "Deportivo Norte", FeeAmount: 10000, FeeDueDay: 5, Currency: "CLP"}
	members := []*membership.TeamMember{
		{MembershipID: "m-player", TeamID: teamID, Role: membership.RolePlayer, Status: "active", PlaysAsPlayer: true},
		{MembershipID: "m-coach", TeamID: teamID, Role: membership.RoleManager, Status: "active", PlaysAsPlayer: false},
		{MembershipID: "m-playing-manager", TeamID: teamID, Role: membership.RoleManager, Status: "active", PlaysAsPlayer: true},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)

	var captured []*fee.Obligation
	feeRepo.On("BulkCreate", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).([]*fee.Obligation)
		}).Return(2, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+teamID+"/generate-fees",
		strings.NewReader(`{"period_year":2026,"period_month":7}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	require.Len(t, captured, 2)
	ids := []string{captured[0].MembershipID, captured[1].MembershipID}
	assert.ElementsMatch(t, []string{"m-player", "m-playing-manager"}, ids)
}

// El invitado de un partido no paga la cuota del club. Juega y paga su cuota de
// cancha, pero la mensualidad es de los socios: cobrársela a alguien que vino un
// sábado es la forma más rápida de que desinstale la app.
func TestGenerateFees_SkipsGuests(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{ID: teamID, Name: "Deportivo Norte", FeeAmount: 10000, FeeDueDay: 5, Currency: "CLP"}
	members := []*membership.TeamMember{
		{MembershipID: "m-player", TeamID: teamID, Role: membership.RolePlayer, Status: "active", PlaysAsPlayer: true},
		// El parche: activo y juega, pero no es del club.
		{
			MembershipID: "m-guest", TeamID: teamID, Role: membership.RolePlayer,
			Kind: membership.KindGuest, Status: "active", PlaysAsPlayer: true,
		},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)

	var captured []*fee.Obligation
	feeRepo.On("BulkCreate", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).([]*fee.Obligation)
		}).Return(1, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+teamID+"/generate-fees",
		strings.NewReader(`{"period_year":2026,"period_month":7}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	require.Len(t, captured, 1)
	assert.Equal(t, "m-player", captured[0].MembershipID)
}

func TestGenerateFees_SkipsInactiveMembers(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{ID: teamID, FeeAmount: 5000, FeeDueDay: 1, Currency: "USD"}
	members := []*membership.TeamMember{
		{MembershipID: "m-1", Status: "inactive", PlaysAsPlayer: true},
		{MembershipID: "m-2", Status: "suspended", PlaysAsPlayer: true},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"period_year":2026,"period_month":1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(0), body["created"])
	// BulkCreate no debe llamarse si no hay activos
	feeRepo.AssertNotCalled(t, "BulkCreate")
}

func TestGenerateFees_TeamNotFound(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamRepo.On("FindByID", mock.Anything, "nonexistent").Return(nil, team.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent"}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"period_year":2026,"period_month":7}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGenerateFees_FeeUsesTeamConfig(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{
		ID: teamID, FeeAmount: 25000, FeeDueDay: 15, Currency: "CLP",
	}
	members := []*membership.TeamMember{
		{MembershipID: "m-1", Status: "active", PlaysAsPlayer: true},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)

	var capturedObs []*fee.Obligation
	feeRepo.On("BulkCreate", mock.Anything, mock.MatchedBy(func(obs []*fee.Obligation) bool {
		capturedObs = obs
		return true
	})).Return(1, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"period_year":2026,"period_month":3}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	require.Len(t, capturedObs, 1)
	o := capturedObs[0]
	assert.Equal(t, int64(25000), o.Amount)
	assert.Equal(t, "CLP", o.Currency)
	assert.Equal(t, 2026, o.PeriodYear)
	assert.Equal(t, 3, o.PeriodMonth)
	assert.Equal(t, time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC), o.DueDate)
}

func TestUpdateFeeStatus_PaidSetsTimestamp(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	h := newFeeHandler(feeRepo, &testutil.MockMembershipRepo{}, &testutil.MockTeamRepo{})

	feeID := "fee-1"
	updated := &fee.Obligation{ID: feeID, Status: fee.StatusPaid}

	feeRepo.On("UpdateStatus", mock.Anything, feeID, fee.StatusPaid, mock.AnythingOfType("*time.Time")).Return(nil)
	feeRepo.On("FindByID", mock.Anything, feeID).Return(updated, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: feeID}}
	withClaims(c, "tesorero")
	c.Request = httptest.NewRequest(http.MethodPatch, "/fees/"+feeID+"/status",
		strings.NewReader(`{"status":"paid"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Verifica que paid_at no sea nil cuando status=paid
	feeRepo.AssertCalled(t, "UpdateStatus", mock.Anything, feeID, fee.StatusPaid, mock.AnythingOfType("*time.Time"))
}

// Las cuotas del equipo no las ve alguien de afuera. (Un jugador del equipo
// todavía sí: ver el comentario de `ListByTeamAndPeriod`.)
func TestListFeesByTeam_RejectsOutsider(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	memberRepo.On("FindByUserAndTeam", mock.Anything, "forastero", "team-1").
		Return(nil, membership.ErrNotFound)
	h := handler.NewFeeHandler(feeRepo, memberRepo, &testutil.MockTeamRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	withClaims(c, "forastero")
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/team-1/fees?year=2026&month=10", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	feeRepo.AssertNotCalled(t, "ListByTeamAndPeriod")
}

// La cuota propia sí se ve, aunque quien la mira sea jugador.
func TestListFeesByMembership_AllowsOwner(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	memberRepo.On("GetMemberByID", mock.Anything, "m-jugador").
		Return(&membership.TeamMember{MembershipID: "m-jugador", TeamID: "team-1"}, nil)
	memberRepo.On("FindByUserAndTeam", mock.Anything, "jugador", "team-1").
		Return(playerOf("team-1", "jugador"), nil)
	feeRepo.On("ListByMembership", mock.Anything, "m-jugador").Return([]*fee.Obligation{}, nil)
	h := handler.NewFeeHandler(feeRepo, memberRepo, &testutil.MockTeamRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "m-jugador"}}
	withClaims(c, "jugador")
	c.Request = httptest.NewRequest(http.MethodGet, "/memberships/m-jugador/fees", nil)

	h.ListByMembership(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// La de un compañero, no.
func TestListFeesByMembership_RejectsTeammate(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	memberRepo.On("GetMemberByID", mock.Anything, "m-otro").
		Return(&membership.TeamMember{MembershipID: "m-otro", TeamID: "team-1"}, nil)
	memberRepo.On("FindByUserAndTeam", mock.Anything, "jugador", "team-1").
		Return(playerOf("team-1", "jugador"), nil)
	h := handler.NewFeeHandler(feeRepo, memberRepo, &testutil.MockTeamRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "m-otro"}}
	withClaims(c, "jugador")
	c.Request = httptest.NewRequest(http.MethodGet, "/memberships/m-otro/fees", nil)

	h.ListByMembership(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	feeRepo.AssertNotCalled(t, "ListByMembership")
}

// Marcar una cuota como pagada es cobrarla: el dueño no puede hacerlo solo.
func TestUpdateFeeStatus_RejectsPlayer(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	feeRepo.On("FindByID", mock.Anything, "fee-1").
		Return(&fee.Obligation{ID: "fee-1", TeamID: "team-1", MembershipID: "m-jugador"}, nil)
	memberRepo.On("FindByUserAndTeam", mock.Anything, "jugador", "team-1").
		Return(playerOf("team-1", "jugador"), nil)
	h := handler.NewFeeHandler(feeRepo, memberRepo, &testutil.MockTeamRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "fee-1"}}
	withClaims(c, "jugador")
	c.Request = httptest.NewRequest(http.MethodPatch, "/fees/fee-1/status", strings.NewReader(`{"status":"paid"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateStatus(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	feeRepo.AssertNotCalled(t, "UpdateStatus")
}
