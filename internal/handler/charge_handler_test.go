package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func treasurerOf(teamID, userID, membershipID string) *membership.Membership {
	return &membership.Membership{
		ID: membershipID, UserID: userID, TeamID: teamID,
		Role: membership.RoleTreasurer, Status: membership.StatusActive,
	}
}

type chargeDeps struct {
	charges      *testutil.MockChargeRepo
	competitions *testutil.MockCompetitionRepo
	matches      *testutil.MockMatchRepo
	members      *testutil.MockMembershipRepo
	funds        *testutil.MockFundsRepo
}

func newChargeHandler() (*handler.ChargeHandler, chargeDeps) {
	d := chargeDeps{
		charges:      &testutil.MockChargeRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		matches:      &testutil.MockMatchRepo{},
		members:      &testutil.MockMembershipRepo{},
		funds:        &testutil.MockFundsRepo{},
	}
	return handler.NewChargeHandler(d.charges, d.competitions, d.matches, d.members, d.funds, nil), d
}

func sevenASideCompetition() *competition.Competition {
	perSide := 7
	return &competition.Competition{
		ID: "comp-1", Type: competition.TypeFriendly, OrganizerTeamID: homeTeam,
		PlayersPerSide: &perSide,
		VenueCost:      &competition.VenueCost{Amount: 28000, Currency: "CLP"},
	}
}

func splitRequest(c *gin.Context) {
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/charges",
		strings.NewReader(`{"source_type":"match_cost","source_id":"comp-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")
}

// El excedente del reparto queda anotado como fondo del equipo.
func expectFundsSet(d chargeDeps, surplus int64) {
	d.funds.On("Set", mock.Anything, homeTeam,
		funds.Source{Type: funds.SourceMatchCost, ID: "comp-1"}, surplus, "CLP").Return(nil)
}

// El monto por cabeza lo calcula el servidor desde la nómina, no el cliente:
// $28.000 entre 7 por lado × 2 equipos = $2.000 fijos.
func TestSplitCharges_ServerComputesTheAmount(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{
		{MembershipID: "m-1", Status: match.CallupConfirmed},
		{MembershipID: "m-2", Status: match.CallupConfirmed},
		{MembershipID: "m-3", Status: match.CallupCalled},
		{MembershipID: "m-4", Status: match.CallupDeclined}, // no juega, no paga
	}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-1", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-2", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-3", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-4", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)

	d.charges.On("CreateForSource", mock.Anything, mock.MatchedBy(func(in charge.CreateInput) bool {
		if len(in.Lines) != 3 {
			return false
		}
		for _, line := range in.Lines {
			if line.Amount != 2000 || line.MembershipID == "m-4" {
				return false
			}
		}
		return true
	})).Return([]*charge.Charge{}, nil)
	// 3 pagan × $2.000 = $6.000, contra una mitad de $14.000: faltan $8.000,
	// que el equipo absorbe como fondo negativo.
	expectFundsSet(d, -8000)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(2000), body["per_player"])
	assert.Equal(t, float64(14000), body["team_share"])
	assert.Equal(t, float64(-8000), body["surplus"])

	d.charges.AssertExpectations(t)
	d.funds.AssertExpectations(t)
}

// Los montos que mande el cliente son irrelevantes: el servidor los ignora.
func TestSplitCharges_IgnoresClientSuppliedAmounts(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{
		{MembershipID: "m-1", Status: match.CallupConfirmed},
	}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-1", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)
	d.charges.On("CreateForSource", mock.Anything, mock.MatchedBy(func(in charge.CreateInput) bool {
		return len(in.Lines) == 1 && in.Lines[0].Amount == 2000
	})).Return([]*charge.Charge{}, nil)
	expectFundsSet(d, -12000)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	// El cliente intenta cobrar $1 y sumar a alguien de otro equipo.
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/charges",
		strings.NewReader(`{"source_type":"match_cost","source_id":"comp-1",
		                    "lines":[{"membership_id":"m-ajeno","amount":1}],"total_amount":1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Split(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.charges.AssertExpectations(t)
	d.funds.AssertExpectations(t)
}

// Un id de otro equipo colado en la convocatoria no genera deuda.
func TestSplitCharges_SkipsCallupsFromAnotherTeam(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{
		{MembershipID: "m-1", Status: match.CallupConfirmed},
		{MembershipID: "m-del-rival", Status: match.CallupConfirmed},
	}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-1", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)
	d.charges.On("CreateForSource", mock.Anything, mock.MatchedBy(func(in charge.CreateInput) bool {
		return len(in.Lines) == 1 && in.Lines[0].MembershipID == "m-1"
	})).Return([]*charge.Charge{}, nil)
	expectFundsSet(d, -12000)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.charges.AssertExpectations(t)
	d.funds.AssertExpectations(t)
}

// Sin convocatoria no hay a quién cobrarle.
func TestSplitCharges_RequiresCallups(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	d.charges.AssertNotCalled(t, "CreateForSource", mock.Anything, mock.Anything)
	d.funds.AssertNotCalled(t, "Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Sin partido confirmado todavía no se reparte nada.
func TestSplitCharges_RequiresAMatch(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	d.charges.AssertNotCalled(t, "CreateForSource", mock.Anything, mock.Anything)
	d.funds.AssertNotCalled(t, "Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador raso no reparte costos.
func TestSplitCharges_PlayerCannotSplit(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.competitions.AssertNotCalled(t, "FindByID", mock.Anything, mock.Anything)
	d.funds.AssertNotCalled(t, "Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// El comprobante lo sube el propio deudor: nadie paga por otro.
func TestSubmitReceipt_RejectsSomeoneElsesCharge(t *testing.T) {
	h, d := newChargeHandler()

	d.charges.On("FindByID", mock.Anything, "charge-1").Return(&charge.Charge{
		ID: "charge-1", TeamID: homeTeam, MembershipID: "m-otro",
		Amount: 2000, Currency: "CLP", Status: charge.StatusPending,
	}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-mia", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "chargeId", Value: "charge-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/charge-1/receipt",
		strings.NewReader(`{"receipt_url":"https://x/y.jpg"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.SubmitReceipt(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.charges.AssertNotCalled(t, "SubmitReceipt", mock.Anything, mock.Anything, mock.Anything)
}

// Un tesorero no puede confirmarse su propio pago: eso elimina el control de
// dos ojos que justifica que exista el estado 'submitted'.
func TestConfirmCharge_RejectsSelfConfirmation(t *testing.T) {
	h, d := newChargeHandler()

	d.charges.On("FindByID", mock.Anything, "charge-1").Return(&charge.Charge{
		ID: "charge-1", TeamID: homeTeam, MembershipID: "m-tesorero",
		Amount: 2000, Currency: "CLP", Status: charge.StatusSubmitted,
	}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-tes", homeTeam).
		Return(treasurerOf(homeTeam, "user-tes", "m-tesorero"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-tes")
	c.Params = gin.Params{{Key: "chargeId", Value: "charge-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/charge-1/confirm", nil)

	h.Confirm(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "someone else")
	d.charges.AssertNotCalled(t, "Confirm", mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador no puede confirmar pagos ajenos.
func TestConfirmCharge_PlayerCannotConfirm(t *testing.T) {
	h, d := newChargeHandler()

	d.charges.On("FindByID", mock.Anything, "charge-1").Return(&charge.Charge{
		ID: "charge-1", TeamID: homeTeam, MembershipID: "m-otro",
		Status: charge.StatusSubmitted,
	}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-mia", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "chargeId", Value: "charge-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/charge-1/confirm", nil)

	h.Confirm(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.charges.AssertNotCalled(t, "Confirm", mock.Anything, mock.Anything, mock.Anything)
}

// Más jugadores que la nómina: el excedente queda anotado como fondo del equipo.
func TestSplitCharges_RecordsSurplusAsTeamFunds(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	var callups []*match.Callup
	members := make([]*membership.TeamMember, 0, 10)
	for i := 1; i <= 10; i++ {
		callups = append(callups, &match.Callup{MembershipID: fmt.Sprintf("m-%d", i), Status: match.CallupConfirmed})
		members = append(members, &membership.TeamMember{MembershipID: fmt.Sprintf("m-%d", i), TeamID: homeTeam, Status: membership.StatusActive})
	}
	d.matches.On("ListCallups", mock.Anything, "match-1").Return(callups, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return(members, nil)
	d.charges.On("CreateForSource", mock.Anything, mock.Anything).Return([]*charge.Charge{}, nil)
	// 10 × $2.000 = $20.000, contra $14.000 de mitad: $6.000 para el equipo.
	expectFundsSet(d, 6000)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.funds.AssertExpectations(t)
}

// Si el reparto no deja excedente, la entrada de fondos se borra: que un partido
// no sobrara plata no deja un registro fantasma en la lista.
func TestSplitCharges_RemovesFundsWhenNothingLeftOver(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	var callups []*match.Callup
	members := make([]*membership.TeamMember, 0, 7)
	for i := 1; i <= 7; i++ {
		callups = append(callups, &match.Callup{MembershipID: fmt.Sprintf("m-%d", i), Status: match.CallupConfirmed})
		members = append(members, &membership.TeamMember{MembershipID: fmt.Sprintf("m-%d", i), TeamID: homeTeam, Status: membership.StatusActive})
	}
	d.matches.On("ListCallups", mock.Anything, "match-1").Return(callups, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return(members, nil)
	d.charges.On("CreateForSource", mock.Anything, mock.Anything).Return([]*charge.Charge{}, nil)
	// 7 × $2.000 = $14.000 = la mitad exacta: excedente cero.
	expectFundsSet(d, 0)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	splitRequest(c)

	h.Split(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	d.funds.AssertExpectations(t)
}

// Los fondos del equipo se listan con el nombre del partido que los dejó.
func TestFunds_ListsTeamFunds(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)
	perSide := 7
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(&competition.Competition{
		ID: "comp-1", Name: "Amistoso vs Los Rayos", Type: competition.TypeFriendly,
		OrganizerTeamID: homeTeam, PlayersPerSide: &perSide,
	}, nil)
	d.funds.On("ListByTeam", mock.Anything, homeTeam).Return([]*funds.Entry{
		{
			TeamID: homeTeam,
			Source: funds.Source{Type: funds.SourceMatchCost, ID: "comp-1"},
			Amount: 6000, Currency: "CLP",
		},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+homeTeam+"/funds", nil)

	h.Funds(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Total   int64 `json:"total"`
		Entries []struct {
			Name   string `json:"name"`
			Amount int64  `json:"amount"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(6000), body.Total)
	require.Len(t, body.Entries, 1)
	assert.Equal(t, "Amistoso vs Los Rayos", body.Entries[0].Name)
	assert.Equal(t, int64(6000), body.Entries[0].Amount)

	d.funds.AssertExpectations(t)
}

// La deuda de otro equipo no se filtra: el endpoint pide el equipo del propio
// miembro.
func TestFunds_RequiresMembership(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+homeTeam+"/funds", nil)

	h.Funds(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.funds.AssertNotCalled(t, "ListByTeam", mock.Anything, mock.Anything)
}
