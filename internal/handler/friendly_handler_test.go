package handler_test

import (
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

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	fr.AssertExpectations(t)
	mr.AssertExpectations(t)
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
