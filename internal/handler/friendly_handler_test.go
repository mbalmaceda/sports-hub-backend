package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

const (
	homeTeam = "team-home"
	awayTeam = "team-away"
)

// withClaims arma el contexto como lo dejaría el middleware de auth.
func withClaims(c *gin.Context, userID string) {
	c.Set("claims", &auth.Claims{UserID: userID, RegisteredClaims: jwt.RegisteredClaims{}})
}

func managerOf(teamID, userID string) *membership.Membership {
	return &membership.Membership{
		ID:     "m-" + userID,
		UserID: userID,
		TeamID: teamID,
		Role:   membership.RoleManager,
		Status: membership.StatusActive,
	}
}

func openChallenge() *friendly.Challenge {
	return &friendly.Challenge{
		ID:               "ch-1",
		CompetitionID:    "comp-1",
		ChallengerTeamID: homeTeam,
		ChallengedTeamID: awayTeam,
		Status:           friendly.StatusPending,
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	}
}

func newFriendlyHandler(
	fr *testutil.MockFriendlyRepo,
	cr *testutil.MockCompetitionRepo,
	mr *testutil.MockMatchRepo,
	memr *testutil.MockMembershipRepo,
) *handler.FriendlyHandler {
	return handler.NewFriendlyHandler(fr, cr, mr, memr)
}

// El equipo que hizo la última propuesta no puede aceptarla. Sin este chequeo
// un manager cerraría un partido que el rival nunca confirmó.
func TestAcceptFriendly_RejectsAcceptingYourOwnProposal(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	ch := openChallenge()
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", awayTeam).
		Return(nil, membership.ErrNotFound)
	// La última propuesta es del propio equipo que intenta aceptar.
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: homeTeam,
		ProposedStartAt: time.Now().Add(72 * time.Hour),
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "waiting for the other team")
	// Lo importante: no se creó ningún partido.
	mr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// El rival sí puede aceptar, y eso crea el partido confirmado.
func TestAcceptFriendly_OpponentAcceptsAndMatchIsCreated(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	ch := openChallenge()
	kickoff := time.Now().Add(72 * time.Hour)

	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", homeTeam).
		Return(nil, membership.ErrNotFound)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: homeTeam,
		ProposedStartAt: kickoff, ProposedVenue: "Complejo Municipal",
	}, nil)
	fr.On("UpdateStatus", mock.Anything, ch.ID, friendly.StatusAccepted).Return(nil)
	mr.On("Create", mock.Anything, mock.MatchedBy(func(m *match.Match) bool {
		// El partido hereda fecha y lugar de la última propuesta, y nace confirmado.
		return m.Status == match.StatusConfirmed &&
			m.Venue == "Complejo Municipal" &&
			m.HomeTeamID == homeTeam && m.AwayTeamID == awayTeam
	})).Return(nil)
	// Los dos equipos quedan activos en la competencia.
	cr.On("UpsertEntry", mock.Anything, mock.Anything).Return(nil).Twice()
	cr.On("UpdateStatus", mock.Anything, ch.CompetitionID, mock.Anything).Return(nil)
	// La competencia se alinea con la propuesta aceptada: si hubo contraoferta,
	// la fecha con la que nació ya no es la que se juega.
	cr.On("UpdateSchedule", mock.Anything, ch.CompetitionID, kickoff, "Complejo Municipal").Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	fr.AssertExpectations(t)
	mr.AssertExpectations(t)
	// Sin esto, que la competencia nunca se reagendara pasaría desapercibido.
	cr.AssertExpectations(t)
}

// Un jugador del equipo rival no puede aceptar por su equipo: hace falta manager.
func TestAcceptFriendly_PlayerCannotAccept(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	ch := openChallenge()
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(nil, membership.ErrNotFound)
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", awayTeam).Return(&membership.Membership{
		ID: "m-player", UserID: "user-player", TeamID: awayTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Un desafío vencido no se puede aceptar aunque siga en 'pending'.
func TestAcceptFriendly_ExpiredIsRejected(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	ch := openChallenge()
	ch.ExpiresAt = time.Now().Add(-time.Hour)
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "expired")
}

// No se puede desafiar al propio equipo.
func TestCreateFriendly_RejectsSelfChallenge(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)

	body := `{"challenged_team_id":"` + homeTeam + `","name":"X vs X","sport_id":"football",` +
		`"proposed_start_at":"` + time.Now().Add(48*time.Hour).Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/friendlies", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Listar los amistosos del equipo vence lo que se pasó de plazo y cancela su
// competencia. Es lo único que lo hace: sin un trabajo periódico, si la lectura
// no barre, el desafío se queda en 'pending' para siempre.
func TestListFriendlies_ExpiresStaleAndCancelsCompetition(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	fr.On("ExpireStale", mock.Anything, mock.Anything).Return([]string{"comp-1"}, nil)
	cr.On("UpdateStatus", mock.Anything, "comp-1", competition.StatusCancelled).Return(nil)
	fr.On("ListByTeam", mock.Anything, homeTeam).Return([]*friendly.Challenge{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+homeTeam+"/friendlies", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusOK, w.Code)
	cr.AssertExpectations(t)
}

// Si la barrida falla, la lectura sigue: el peor caso es devolver un estado
// viejo, no una pantalla de error.
func TestListFriendlies_SweepFailureDoesNotBreakTheRead(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	fr.On("ExpireStale", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	fr.On("ListByTeam", mock.Anything, homeTeam).Return([]*friendly.Challenge{openChallenge()}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/"+homeTeam+"/friendlies", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ch-1")
	cr.AssertNotCalled(t, "UpdateStatus", mock.Anything, mock.Anything, mock.Anything)
}

// El partido interno nace completo: competencia activa, el equipo adentro y el
// partido confirmado con el mismo equipo de los dos lados. Sin desafío de por
// medio, porque no hay a quién esperar.
func TestCreateInternalMatch_CreatesCompetitionAndConfirmedMatch(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	cr.On("Create", mock.Anything, mock.Anything).Return(nil)
	cr.On("UpsertEntry", mock.Anything, mock.Anything).Return(nil)
	mr.On("Create", mock.Anything, mock.Anything).Return(nil)

	body := `{"name":"Pichanga del jueves","sport_id":"football7","players_per_side":7,` +
		`"venue":"Cancha 3","start_at":"` + time.Now().Add(48*time.Hour).Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/internal-matches", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInternal(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	createdComp := cr.Calls[0].Arguments.Get(1).(*competition.Competition)
	assert.True(t, createdComp.IsInternal)
	assert.Equal(t, competition.TypeFriendly, createdComp.Type)
	// Activa desde el arranque: no hay rival que tenga que aceptar.
	assert.Equal(t, competition.StatusActive, createdComp.Status)

	createdMatch := mr.Calls[0].Arguments.Get(1).(*match.Match)
	assert.Equal(t, homeTeam, createdMatch.HomeTeamID)
	assert.Equal(t, homeTeam, createdMatch.AwayTeamID)
	assert.Equal(t, match.StatusConfirmed, createdMatch.Status)

	// Nadie a quien desafiar: no se crea desafío ni propuesta.
	fr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador no puede armar un partido interno: convocar y repartir el costo de
// la cancha es trabajo del manager, igual que en un amistoso.
func TestCreateInternalMatch_PlayerCannotCreate(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	player := managerOf(homeTeam, "user-player")
	player.Role = membership.RolePlayer
	memr.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(player, nil)

	body := `{"name":"Pichanga","sport_id":"football7",` +
		`"start_at":"` + time.Now().Add(48*time.Hour).Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/internal-matches", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInternal(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Una fecha ya pasada se rechaza antes de escribir nada.
func TestCreateInternalMatch_RejectsPastDate(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)

	body := `{"name":"Pichanga","sport_id":"football7",` +
		`"start_at":"` + time.Now().Add(-2*time.Hour).Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/internal-matches", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInternal(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}
