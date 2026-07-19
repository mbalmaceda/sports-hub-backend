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

func newFeeHandler(feeRepo *testutil.MockFeeRepo, memberRepo *testutil.MockMembershipRepo, teamRepo *testutil.MockTeamRepo) *handler.FeeHandler {
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
		{MembershipID: "m-1", UserID: "u-1", TeamID: teamID, Status: "active"},
		{MembershipID: "m-2", UserID: "u-2", TeamID: teamID, Status: "active"},
		{MembershipID: "m-3", UserID: "u-3", TeamID: teamID, Status: "inactive"},
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

func TestGenerateFees_SkipsInactiveMembers(t *testing.T) {
	feeRepo := &testutil.MockFeeRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	teamRepo := &testutil.MockTeamRepo{}
	h := newFeeHandler(feeRepo, memberRepo, teamRepo)

	teamID := "team-1"
	t1 := &team.Team{ID: teamID, FeeAmount: 5000, FeeDueDay: 1, Currency: "USD"}
	members := []*membership.TeamMember{
		{MembershipID: "m-1", Status: "inactive"},
		{MembershipID: "m-2", Status: "suspended"},
	}

	teamRepo.On("FindByID", mock.Anything, teamID).Return(t1, nil)
	memberRepo.On("ListByTeam", mock.Anything, teamID).Return(members, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: teamID}}
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
		{MembershipID: "m-1", Status: "active"},
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
	h := handler.NewFeeHandler(feeRepo, &testutil.MockMembershipRepo{}, &testutil.MockTeamRepo{})

	feeID := "fee-1"
	updated := &fee.Obligation{ID: feeID, Status: fee.StatusPaid}

	feeRepo.On("UpdateStatus", mock.Anything, feeID, fee.StatusPaid, mock.AnythingOfType("*time.Time")).Return(nil)
	feeRepo.On("FindByID", mock.Anything, feeID).Return(updated, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: feeID}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/fees/"+feeID+"/status",
		strings.NewReader(`{"status":"paid"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Verifica que paid_at no sea nil cuando status=paid
	feeRepo.AssertCalled(t, "UpdateStatus", mock.Anything, feeID, fee.StatusPaid, mock.AnythingOfType("*time.Time"))
}
