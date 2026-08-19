package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func newTeamHandler(teamRepo *testutil.MockTeamRepo, memberRepo *testutil.MockMembershipRepo) *handler.TeamHandler {
	return handler.NewTeamHandler(teamRepo, memberRepo, nil)
}

func TestCreateTeam_Success(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	h := newTeamHandler(teamRepo, memberRepo)

	teamRepo.On("Create", mock.Anything, mock.AnythingOfType("*team.Team")).Return(nil)
	memberRepo.On("Create", mock.Anything, mock.MatchedBy(func(m *membership.Membership) bool {
		return m.Role == membership.RoleManager && m.UserID == "creator-user"
	})).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "creator-user")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Deportivo","sport_id":"football","category":"Senior","fee_due_day":5,"currency":"CLP"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Deportivo", body["name"])
	assert.Equal(t, true, body["is_active"])

	// Verifica que el creador queda como manager
	memberRepo.AssertExpectations(t)
}

func TestCreateTeam_CreatorBecomesManager(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	h := newTeamHandler(teamRepo, memberRepo)

	var capturedMembership *membership.Membership
	teamRepo.On("Create", mock.Anything, mock.AnythingOfType("*team.Team")).Return(nil)
	memberRepo.On("Create", mock.Anything, mock.AnythingOfType("*membership.Membership")).
		Run(func(args mock.Arguments) {
			capturedMembership = args.Get(1).(*membership.Membership)
		}).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-abc")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Club","sport_id":"basketball","category":"U18","fee_due_day":1,"currency":"USD"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	require.NotNil(t, capturedMembership)
	assert.Equal(t, "user-abc", capturedMembership.UserID)
	assert.Equal(t, membership.RoleManager, capturedMembership.Role)
	assert.Equal(t, membership.StatusActive, capturedMembership.Status)
	// Sin el campo en el body, el manager no ocupa lugar en la plantilla: es lo
	// que hacía la app antes de que el flag existiera.
	assert.False(t, capturedMembership.PlaysAsPlayer)
}

// El que arma el equipo muchas veces también juega. Si lo marca en el alta, su
// membresía entra a la plantilla: se lo convoca y se le cobra cuota.
func TestCreateTeam_ManagerWhoAlsoPlays(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	h := newTeamHandler(teamRepo, memberRepo)

	var capturedMembership *membership.Membership
	teamRepo.On("Create", mock.Anything, mock.AnythingOfType("*team.Team")).Return(nil)
	memberRepo.On("Create", mock.Anything, mock.AnythingOfType("*membership.Membership")).
		Run(func(args mock.Arguments) {
			capturedMembership = args.Get(1).(*membership.Membership)
		}).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-abc")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Club","sport_id":"football","category":"Senior","fee_due_day":5,"currency":"CLP","plays_as_player":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	require.NotNil(t, capturedMembership)
	assert.Equal(t, membership.RoleManager, capturedMembership.Role)
	assert.True(t, capturedMembership.PlaysAsPlayer)
}

func TestCreateTeam_ValidationError(t *testing.T) {
	h := newTeamHandler(&testutil.MockTeamRepo{}, &testutil.MockMembershipRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	// sport_id y category son required
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Solo nombre"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateTeam_MembershipFails(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	memberRepo := &testutil.MockMembershipRepo{}
	h := newTeamHandler(teamRepo, memberRepo)

	teamRepo.On("Create", mock.Anything, mock.AnythingOfType("*team.Team")).Return(nil)
	memberRepo.On("Create", mock.Anything, mock.AnythingOfType("*membership.Membership")).Return(assert.AnError)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Club","sport_id":"tennis","category":"Senior","fee_due_day":1,"currency":"USD"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateTeam_NameTaken(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	teamRepo.On("Create", mock.Anything, mock.AnythingOfType("*team.Team")).Return(team.ErrNameTaken)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams",
		strings.NewReader(`{"name":"Deportivo","sport_id":"football","category":"Senior","fee_due_day":1,"currency":"CLP"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestGetTeamByID_Success(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	expected := &team.Team{ID: "team-1", Name: "Los Guerreros", Currency: "CLP"}
	teamRepo.On("FindByID", mock.Anything, "team-1").Return(expected, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/team-1", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Los Guerreros", body["name"])
}

func TestGetTeamByID_NotFound(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	teamRepo.On("FindByID", mock.Anything, "ghost").Return(nil, team.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "ghost"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/ghost", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListTeams_ReturnsEmptyArray(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	teamRepo.On("List", mock.Anything).Return([]*team.Team{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/teams", nil)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotNil(t, body)
	assert.Empty(t, body)
}

func TestListTeams_ReturnsTeams(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	teams := []*team.Team{
		{ID: "t-1", Name: "Alpha"},
		{ID: "t-2", Name: "Beta"},
	}
	teamRepo.On("List", mock.Anything).Return(teams, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/teams", nil)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body, 2)
}

func TestUpdateFeeConfig_Success(t *testing.T) {
	teamRepo := &testutil.MockTeamRepo{}
	h := newTeamHandler(teamRepo, &testutil.MockMembershipRepo{})

	expected := &team.Team{ID: "team-1", FeeAmount: 20000, FeeDueDay: 10}
	teamRepo.On("UpdateFeeConfig", mock.Anything, "team-1",
		team.FeeConfig{FeeAmount: 20000, FeeDueDay: 10}).Return(nil)
	teamRepo.On("FindByID", mock.Anything, "team-1").Return(expected, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/teams/team-1/fee-config",
		strings.NewReader(`{"fee_amount":20000,"fee_due_day":10}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateFeeConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(20000), body["fee_amount"])
}

func TestUpdateFeeConfig_InvalidDueDay(t *testing.T) {
	h := newTeamHandler(&testutil.MockTeamRepo{}, &testutil.MockMembershipRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"fee_amount":10000,"fee_due_day":32}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateFeeConfig(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

/*
Poner la cuota mensual en cero es apagarla, y tiene que poder hacerse.

Con `int64` a secas, el `binding:"required"` de gin rechazaba el cero y un
equipo que activó la cuota una vez no podía volver atrás desde la app. Importa
más desde que la pestaña de Cuotas se muestra solo si hay cuota configurada: sin
esto, esa sección no se puede apagar nunca.
*/
func TestUpdateFeeConfig_ZeroTurnsTheFeeOff(t *testing.T) {
	tr := &testutil.MockTeamRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := handler.NewTeamHandler(tr, memr, nil)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	tr.On("UpdateFeeConfig", mock.Anything, homeTeam, mock.MatchedBy(func(c team.FeeConfig) bool {
		return c.FeeAmount == 0 && c.FeeDueDay == 5
	})).Return(nil)
	tr.On("FindByID", mock.Anything, homeTeam).
		Return(&team.Team{ID: homeTeam, Name: "Clan"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/teams/"+homeTeam+"/fee-config",
		strings.NewReader(`{"fee_amount":0,"fee_due_day":5}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateFeeConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	tr.AssertExpectations(t)
}
