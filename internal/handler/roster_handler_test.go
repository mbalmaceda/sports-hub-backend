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
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func newRosterHandler(repo *testutil.MockMembershipRepo) *handler.RosterHandler {
	return handler.NewRosterHandler(repo, nil)
}

func TestListByTeam_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	members := []*membership.TeamMember{
		{MembershipID: "m-1", FullName: "Ana García", Role: membership.RolePlayer},
		{MembershipID: "m-2", FullName: "Luis Pérez", Role: membership.RoleManager},
	}
	repo.On("ListByTeam", mock.Anything, "team-1").Return(members, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/team-1/roster", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body, 2)
	assert.Equal(t, "Ana García", body[0]["full_name"])
}

func TestListByTeam_ReturnsEmptyArray(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("ListByTeam", mock.Anything, "team-1").Return([]*membership.TeamMember{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/team-1/roster", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotNil(t, body)
	assert.Empty(t, body)
}

func TestAddMember_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	member := &membership.TeamMember{
		MembershipID: "m-new",
		UserID:       "user-1",
		TeamID:       "team-1",
		Role:         membership.RolePlayer,
		Status:       membership.StatusActive,
	}

	repo.On("FindByUserAndTeam", mock.Anything, "user-1", "team-1").Return(nil, membership.ErrNotFound)
	repo.On("Create", mock.Anything, mock.AnythingOfType("*membership.Membership")).Return(nil)
	repo.On("GetMemberByID", mock.Anything, "").Return(member, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/team-1/roster",
		strings.NewReader(`{"user_id":"user-1","jersey_number":9,"position":"Mediocampista"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AddMember(c)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddMember_DefaultsToPlayerRole(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	var capturedMembership *membership.Membership
	repo.On("FindByUserAndTeam", mock.Anything, "user-1", "team-1").Return(nil, membership.ErrNotFound)
	repo.On("Create", mock.Anything, mock.AnythingOfType("*membership.Membership")).
		Run(func(args mock.Arguments) {
			capturedMembership = args.Get(1).(*membership.Membership)
		}).Return(nil)
	repo.On("GetMemberByID", mock.Anything, mock.Anything).
		Return(&membership.TeamMember{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	// No incluye "role" en el body
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"user_id":"user-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AddMember(c)

	require.NotNil(t, capturedMembership)
	assert.Equal(t, membership.RolePlayer, capturedMembership.Role)
}

func TestAddMember_DuplicateConflict(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	existing := &membership.Membership{ID: "m-existing"}
	repo.On("FindByUserAndTeam", mock.Anything, "user-1", "team-1").Return(existing, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"user_id":"user-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AddMember(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	repo.AssertNotCalled(t, "Create")
}

func TestAddMember_InvalidRole(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("FindByUserAndTeam", mock.Anything, "user-1", "team-1").Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"user_id":"user-1","role":"superadmin"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AddMember(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	repo.AssertNotCalled(t, "Create")
}

func TestGetMember_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	member := &membership.TeamMember{MembershipID: "m-1", FullName: "Carlos López"}
	repo.On("GetMemberByID", mock.Anything, "m-1").Return(member, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}, {Key: "membershipId", Value: "m-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetMember(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Carlos López", body["full_name"])
}

func TestGetMember_NotFound(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("GetMemberByID", mock.Anything, "ghost").Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "ghost"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetMember(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateStatus_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("UpdateStatus", mock.Anything, "m-1", membership.StatusSuspended).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "m-1"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"status":"suspended"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}

func TestUpdateRole_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	promoted := &membership.TeamMember{MembershipID: "m-1", Role: membership.RoleTreasurer}
	repo.On("UpdateRole", mock.Anything, "m-1", membership.RoleTreasurer).Return(nil)
	repo.On("GetMemberByID", mock.Anything, "m-1").Return(promoted, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "m-1"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"role":"treasurer"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateRole(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "treasurer", body["role"])
}

func TestUpdateRole_InvalidRole(t *testing.T) {
	h := newRosterHandler(&testutil.MockMembershipRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "membershipId", Value: "m-1"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"role":"owner"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateRole(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListMine_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	members := []*membership.TeamMember{
		{MembershipID: "m-1", TeamID: "team-1", Role: membership.RoleManager},
		{MembershipID: "m-2", TeamID: "team-2", Role: membership.RolePlayer},
	}
	repo.On("ListByUser", mock.Anything, "me-user").Return(members, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "me-user")
	c.Request = httptest.NewRequest(http.MethodGet, "/me/memberships", nil)

	h.ListMine(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body, 2)
	assert.Equal(t, "manager", body[0]["role"])
}

func TestListMine_ReturnsEmptyWhenNoTeams(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("ListByUser", mock.Anything, "lonely-user").Return([]*membership.TeamMember{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "lonely-user")
	c.Request = httptest.NewRequest(http.MethodGet, "/me/memberships", nil)

	h.ListMine(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body)
}
