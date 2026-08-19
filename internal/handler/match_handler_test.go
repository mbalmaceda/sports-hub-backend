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

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
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
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

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
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

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
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

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
	// Aceptar deja creado su cargo por la cancha, con la cuota que quedó fijada
	// al crear la competencia. Y también con la membresía del token.
	share := int64(2000)
	compr.On("FindByID", mock.Anything, m.CompetitionID).Return(&competition.Competition{
		ID:          m.CompetitionID,
		VenueCost:   &competition.VenueCost{Amount: 28000, Currency: "CLP"},
		PlayerShare: &share,
	}, nil)
	chr.On("EnsureForMembership", mock.Anything, charge.EnsureInput{
		TeamID:       homeTeam,
		MembershipID: "m-mia",
		Source:       charge.Source{Type: charge.SourceMatchCost, ID: m.CompetitionID},
		Amount:       share,
		Currency:     "CLP",
	}).Return(&charge.Charge{ID: "ch-1"}, nil)

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
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

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

// ─── Marcador ────────────────────────────────────────────────────────────────

// playedMatch es el mismo partido pero ya jugado: es lo único que habilita
// cargar un resultado.
func playedMatch() *match.Match {
	m := confirmedMatch()
	m.ScheduledAt = time.Now().Add(-3 * time.Hour)
	return m
}

func resultRequest(c *gin.Context, matchID, body string) {
	c.Params = gin.Params{{Key: "matchId", Value: matchID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/matches/"+matchID+"/result",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
}

// El 0 a 0 es un resultado, no un campo vacío: tiene que poder cargarse.
// Es lo que obliga a que los goles entren como punteros en el body.
func TestSaveResult_AcceptsGoalless(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	mr.On("SaveResult", mock.Anything, m.ID, mock.MatchedBy(func(r match.Result) bool {
		return r.HomeScore == 0 && r.AwayScore == 0 && r.RecordedBy == "user-home"
	})).Return(playedMatch(), nil)
	// El cierre de la competencia es aparte y ya tiene su propio test.
	compr.On("FindByID", mock.Anything, m.CompetitionID).
		Return(&competition.Competition{ID: m.CompetitionID, Status: competition.StatusCancelled}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":0,"away_score":0}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mr.AssertExpectations(t)
}

// Antes del pitazo inicial no hay resultado que cargar.
func TestSaveResult_RejectsBeforeKickoff(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	// confirmedMatch se juega dentro de 48 horas.
	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":2,"away_score":1}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	mr.AssertNotCalled(t, "SaveResult", mock.Anything, mock.Anything, mock.Anything)
}

// El marcador lo carga el manager: un jugador del plantel no.
func TestSaveResult_PlayerCannotRecord(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":2,"away_score":1}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mr.AssertNotCalled(t, "SaveResult", mock.Anything, mock.Anything, mock.Anything)
}

// El rival también carga el marcador: los dos vieron el partido, y esperar a
// que lo cargue uno solo deja la mitad de los encuentros sin resultado.
func TestSaveResult_AwayManagerCanRecord(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	mr.On("SaveResult", mock.Anything, m.ID, mock.Anything).Return(playedMatch(), nil)
	compr.On("FindByID", mock.Anything, m.CompetitionID).
		Return(&competition.Competition{ID: m.CompetitionID, Status: competition.StatusFinished}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	resultRequest(c, m.ID, `{"team_id":"`+awayTeam+`","home_score":1,"away_score":3}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mr.AssertExpectations(t)
}

// Un marcador de tres dígitos para arriba es el dedo apoyado en el teclado, no
// un partido: se corta acá para que no desborde el SMALLINT.
func TestSaveResult_RejectsAbsurdScore(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":99999,"away_score":0}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mr.AssertNotCalled(t, "SaveResult", mock.Anything, mock.Anything, mock.Anything)
}

// El amistoso cierra su ciclo: cargado el marcador de su único partido, la
// competencia queda terminada y no solo "con la fecha pasada".
func TestSaveResult_ClosesTheCompetition(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	played := playedMatch()
	home, away := 3, 2
	played.HomeScore, played.AwayScore = &home, &away

	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	mr.On("SaveResult", mock.Anything, m.ID, mock.Anything).Return(played, nil)
	compr.On("FindByID", mock.Anything, m.CompetitionID).
		Return(&competition.Competition{ID: m.CompetitionID, Status: competition.StatusActive}, nil)
	mr.On("ListByCompetition", mock.Anything, m.CompetitionID).Return([]*match.Match{played}, nil)
	compr.On("UpdateStatus", mock.Anything, m.CompetitionID, competition.StatusFinished).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":3,"away_score":2}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusOK, w.Code)
	compr.AssertExpectations(t)
}

// Un torneo no termina porque se jugó la primera fecha: mientras quede un
// partido sin resultado, la competencia sigue activa.
func TestSaveResult_KeepsTournamentOpenWhileMatchesRemain(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := playedMatch()
	played := playedMatch()
	home, away := 1, 0
	played.HomeScore, played.AwayScore = &home, &away
	pending := confirmedMatch()
	pending.ID = "match-2"

	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	mr.On("SaveResult", mock.Anything, m.ID, mock.Anything).Return(played, nil)
	compr.On("FindByID", mock.Anything, m.CompetitionID).
		Return(&competition.Competition{ID: m.CompetitionID, Status: competition.StatusActive}, nil)
	mr.On("ListByCompetition", mock.Anything, m.CompetitionID).
		Return([]*match.Match{played, pending}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	resultRequest(c, m.ID, `{"team_id":"`+homeTeam+`","home_score":1,"away_score":0}`)

	h.SaveResult(c)

	assert.Equal(t, http.StatusOK, w.Code)
	compr.AssertNotCalled(t, "UpdateStatus", mock.Anything, mock.Anything, mock.Anything)
}
