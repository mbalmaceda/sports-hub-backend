package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/teamlink"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

type teamLinkDeps struct {
	links   *testutil.MockTeamLinkRepo
	teams   *testutil.MockTeamRepo
	members *testutil.MockMembershipRepo
}

func newTeamLinkHandler() (*handler.OnboardingHandler, *teamLinkDeps) {
	d := &teamLinkDeps{
		links:   &testutil.MockTeamLinkRepo{},
		teams:   &testutil.MockTeamRepo{},
		members: &testutil.MockMembershipRepo{},
	}
	h := handler.NewOnboardingHandler(
		&testutil.MockOnboardingRepo{}, d.teams, d.members, nil, nil, nil, d.links,
		config.Config{PublicBaseURL: "https://zports.test"})
	return h, d
}

func openTeamLink() *teamlink.Invite {
	now := time.Now()
	return &teamlink.Invite{
		ID:              "link-1",
		TeamID:          homeTeam,
		CreatedByUserID: "user-home",
		MaxUses:         30,
		UsedCount:       2,
		ExpiresAt:       now.Add(7 * 24 * time.Hour),
		CreatedAt:       now,
	}
}

// ─── Crear ───────────────────────────────────────────────────────────────────

// El cupo y el plazo los pone el servidor: el cliente no manda nada que los
// pueda agrandar.
func TestCreateTeamLink_ServerSetsCapAndExpiry(t *testing.T) {
	h, d := newTeamLinkHandler()
	d.members.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	d.links.On("Create", mock.Anything, mock.MatchedBy(func(inv *teamlink.Invite) bool {
		return inv.TeamID == homeTeam && inv.MaxUses == 30 &&
			inv.ExpiresAt.After(time.Now().Add(6*24*time.Hour)) &&
			inv.ExpiresAt.Before(time.Now().Add(8*24*time.Hour))
	}), mock.Anything).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/invite-links", nil)

	h.CreateTeamLink(c)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	url, _ := body["url"].(string)
	token, _ := body["token"].(string)
	assert.NotEmpty(t, token)
	assert.Equal(t, "https://zports.test/e/"+token, url, "el enlace del equipo va por /e/, no por /i/")
	d.links.AssertExpectations(t)
}

func TestCreateTeamLink_PlayerCannot(t *testing.T) {
	h, d := newTeamLinkHandler()
	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(&membership.Membership{
			ID: "m-player", UserID: "user-player", TeamID: homeTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/invite-links", nil)

	h.CreateTeamLink(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.links.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
}

// ─── Vista previa pública ────────────────────────────────────────────────────

func TestGetTeamLink_ShowsTheTeam(t *testing.T) {
	h, d := newTeamLinkHandler()
	d.links.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(openTeamLink(), nil)
	d.teams.On("FindByID", mock.Anything, homeTeam).Return(&team.Team{
		ID: homeTeam, Name: "Deportivo Norte", SportID: "football",
		City: "Santiago", Description: "¡Vamos Norte!",
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/team-links/tok", nil)

	h.GetTeamLink(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Deportivo Norte", body["team_name"])
	assert.Equal(t, "¡Vamos Norte!", body["description"])
	assert.EqualValues(t, 28, body["remaining_uses"])
}

// Vencido es 410 y no 404: el que llegó tarde tiene que saber que llegó tarde.
func TestGetTeamLink_RevokedIsGone(t *testing.T) {
	h, d := newTeamLinkHandler()
	revoked := openTeamLink()
	at := time.Now().Add(-time.Minute)
	revoked.RevokedAt = &at
	d.links.On("FindByTokenHash", mock.Anything, mock.Anything).Return(revoked, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/team-links/tok", nil)

	h.GetTeamLink(c)

	assert.Equal(t, http.StatusGone, w.Code)
}

// ─── Canje ───────────────────────────────────────────────────────────────────

func TestAcceptTeamLink_Joins(t *testing.T) {
	h, d := newTeamLinkHandler()
	d.links.On("Redeem", mock.Anything, hashFor("tok"), "user-new", mock.Anything).
		Return(&teamlink.AcceptResult{MembershipID: "m-new", TeamID: homeTeam}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-new")
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/team-links/tok/accept", nil)

	h.AcceptTeamLink(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, homeTeam, body["team_id"])
	assert.Equal(t, "m-new", body["membership_id"])
}

func TestAcceptTeamLink_ErrorsMapToStatus(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
	}{
		"ya es del equipo": {teamlink.ErrAlreadyMember, http.StatusConflict},
		"vencido o lleno":  {teamlink.ErrNotUsable, http.StatusGone},
		"no existe":        {teamlink.ErrNotFound, http.StatusNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, d := newTeamLinkHandler()
			d.links.On("Redeem", mock.Anything, mock.Anything, "user-new", mock.Anything).Return(nil, tc.err)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			contextWithClaims(c, "user-new")
			c.Params = gin.Params{{Key: "token", Value: "tok"}}
			c.Request = httptest.NewRequest(http.MethodPost, "/team-links/tok/accept", nil)

			h.AcceptTeamLink(c)

			assert.Equal(t, tc.status, w.Code)
		})
	}
}

// ─── Anular ──────────────────────────────────────────────────────────────────

// Lo anula el manager del equipo dueño del enlace, no el de otro equipo.
func TestRevokeTeamLink_OnlyItsManager(t *testing.T) {
	h, d := newTeamLinkHandler()
	d.links.On("FindByID", mock.Anything, "link-1").Return(openTeamLink(), nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-away", homeTeam).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-away")
	c.Params = gin.Params{{Key: "linkId", Value: "link-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/team-invite-links/link-1", nil)

	h.RevokeTeamLink(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.links.AssertNotCalled(t, "Revoke", mock.Anything, mock.Anything, mock.Anything)
}
