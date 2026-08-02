package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func confirmedMatch() *match.Match {
	return &match.Match{
		ID:            "match-1",
		CompetitionID: "comp-1",
		HomeTeamID:    homeTeam,
		AwayTeamID:    awayTeam,
		ScheduledAt:   time.Now().Add(48 * time.Hour),
		Status:        match.StatusConfirmed,
	}
}

// Un manager no puede convocar jugadores que no son de su equipo, aunque mande
// los ids en el body.
func TestCallUp_RejectsPlayersFromAnotherTeam(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := handler.NewMatchHandler(mr, memr)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	// El plantel propio solo tiene a m-1; m-intruso es de otro equipo.
	memr.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-1", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/matches/"+m.ID+"/callups",
		strings.NewReader(`{"team_id":"`+homeTeam+`","membership_ids":["m-1","m-intruso"]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CallUp(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mr.AssertNotCalled(t, "CallUp", mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador no puede convocar: hace falta ser manager.
func TestCallUp_PlayerCannotCallUp(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := handler.NewMatchHandler(mr, memr)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/matches/"+m.ID+"/callups",
		strings.NewReader(`{"team_id":"`+homeTeam+`","membership_ids":["m-1"]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CallUp(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mr.AssertNotCalled(t, "CallUp", mock.Anything, mock.Anything, mock.Anything)
}

// La respuesta a la convocatoria usa la membresía del token, no una del body:
// nadie puede confirmar ni rechazar en nombre de un compañero.
func TestRespondToCallup_UsesMembershipFromToken(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := handler.NewMatchHandler(mr, memr)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-mia", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)
	// Se espera "m-mia" —la del token— aunque el body intente mandar otra.
	mr.On("Respond", mock.Anything, m.ID, "m-mia", true).Return(&match.Callup{
		ID: "cu-1", MatchID: m.ID, MembershipID: "m-mia", Status: match.CallupConfirmed,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/matches/"+m.ID+"/callups/respond",
		strings.NewReader(`{"team_id":"`+homeTeam+`","attending":true,"membership_id":"m-de-otro"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RespondToCallup(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mr.AssertExpectations(t)
}

// Un equipo que no juega el partido no puede convocar para él.
func TestCallUp_RejectsTeamNotInMatch(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := handler.NewMatchHandler(mr, memr)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-tercero")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/matches/"+m.ID+"/callups",
		strings.NewReader(`{"team_id":"team-tercero","membership_ids":["m-1"]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CallUp(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	memr.AssertNotCalled(t, "ListByTeam", mock.Anything, mock.Anything)
}
