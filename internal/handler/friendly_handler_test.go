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
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/settlement"
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

// El repo de liquidaciones entra por variádico para no tocar los trece casos
// que no tienen nada que ver con la deuda entre equipos: los que sí, pasan el
// suyo y le ponen expectativas.
func newFriendlyHandler(
	fr *testutil.MockFriendlyRepo,
	cr *testutil.MockCompetitionRepo,
	mr *testutil.MockMatchRepo,
	memr *testutil.MockMembershipRepo,
	sr ...*testutil.MockSettlementRepo,
) *handler.FriendlyHandler {
	settlements := &testutil.MockSettlementRepo{}
	if len(sr) > 0 {
		settlements = sr[0]
	}
	return handler.NewFriendlyHandler(fr, cr, mr, memr, settlements)
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
	// Al aceptar se lee la competencia para saber cuánto le toca al rival. Sin
	// costo de lugar no hay deuda que anotar, que es el caso de este partido.
	cr.On("FindByID", mock.Anything, ch.CompetitionID).
		Return(&competition.Competition{ID: ch.CompetitionID}, nil)

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

/*
El plazo para responder no puede sobrevivir al partido.

Es el caso del amistoso para hoy a la tarde: con las 48 horas fijas de antes, el
desafío vencía pasado mañana. El rival veía un contador que prometía más tiempo
del que existía y, si aceptaba al día siguiente, se creaba un partido con fecha
pasada.
*/
func TestCreateFriendly_DeadlineNeverOutlivesTheMatch(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	kickoff := time.Now().Add(3 * time.Hour).Truncate(time.Second)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	cr.On("Create", mock.Anything, mock.Anything).Return(nil)
	fr.On("Create", mock.Anything, mock.MatchedBy(func(ch *friendly.Challenge) bool {
		return ch.ExpiresAt.Equal(kickoff)
	}), mock.Anything).Return(nil)

	body := `{"challenged_team_id":"` + awayTeam + `","name":"Amistoso","sport_id":"football",` +
		`"proposed_start_at":"` + kickoff.Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/friendlies", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	fr.AssertExpectations(t)
}

// Y cuando el partido es lejano manda el TTL: el techo no acorta de más.
func TestCreateFriendly_DistantMatchKeepsTheFullTTL(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	kickoff := time.Now().Add(30 * 24 * time.Hour)

	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	cr.On("Create", mock.Anything, mock.Anything).Return(nil)
	fr.On("Create", mock.Anything, mock.MatchedBy(func(ch *friendly.Challenge) bool {
		// Los dos días de siempre, con margen para lo que tarda el test.
		return ch.ExpiresAt.After(time.Now().Add(47*time.Hour)) &&
			ch.ExpiresAt.Before(time.Now().Add(49*time.Hour))
	}), mock.Anything).Return(nil)

	body := `{"challenged_team_id":"` + awayTeam + `","name":"Amistoso","sport_id":"football",` +
		`"proposed_start_at":"` + kickoff.Format(time.RFC3339) + `"}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/friendlies", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	fr.AssertExpectations(t)
}

/*
Un desafío cuyo partido ya se jugó no se acepta, aunque su plazo diga que sigue
vivo.

Es la fila vieja: nació antes del techo, con 48 horas fijas sobre un partido que
era esa misma tarde. Sin este corte, aceptarla creaba un partido con fecha
pasada —convocatorias que nadie va a responder y cobros por una cancha que no se
usó— y encima nacía directo en el historial.
*/
func TestAcceptFriendly_MatchAlreadyPlayedIsRejected(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr)

	ch := openChallenge()
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", homeTeam).
		Return(nil, membership.ErrNotFound)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	// El plazo del desafío todavía no venció, pero el partido era ayer.
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: homeTeam,
		ProposedStartAt: time.Now().Add(-24 * time.Hour),
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "expired")
	mr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	fr.AssertNotCalled(t, "UpdateStatus", mock.Anything, mock.Anything, mock.Anything)
}

// ─── La mitad de la cancha que le toca al rival ───────────────────────────────

// acceptSetup deja mockeado todo lo que Accept toca antes de la deuda, para que
// los dos casos de abajo solo tengan que hablar de la deuda.
func acceptSetup(
	fr *testutil.MockFriendlyRepo,
	cr *testutil.MockCompetitionRepo,
	mr *testutil.MockMatchRepo,
	memr *testutil.MockMembershipRepo,
	ch *friendly.Challenge,
) {
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", homeTeam).
		Return(nil, membership.ErrNotFound)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: homeTeam,
		ProposedStartAt: time.Now().Add(72 * time.Hour), ProposedVenue: "Complejo Municipal",
	}, nil)
	fr.On("UpdateStatus", mock.Anything, ch.ID, friendly.StatusAccepted).Return(nil)
	mr.On("Create", mock.Anything, mock.Anything).Return(nil)
	cr.On("UpsertEntry", mock.Anything, mock.Anything).Return(nil).Twice()
	cr.On("UpdateStatus", mock.Anything, ch.CompetitionID, mock.Anything).Return(nil)
	cr.On("UpdateSchedule", mock.Anything, ch.CompetitionID, mock.Anything, mock.Anything).Return(nil)
}

func acceptRequest(ch *friendly.Challenge) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)
	return w, c
}

// Aceptar el amistoso deja anotado que el retado le debe la mitad de la cancha
// al organizador. Es el tramo que faltaba: sin esto el rival le cobra a sus
// jugadores y esa plata nunca llega al que pagó el lugar entero.
func TestAcceptFriendly_RecordsWhatTheRivalOwes(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	sr := &testutil.MockSettlementRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr, sr)

	ch := openChallenge()
	acceptSetup(fr, cr, mr, memr, ch)
	cr.On("FindByID", mock.Anything, ch.CompetitionID).Return(&competition.Competition{
		ID:              ch.CompetitionID,
		OrganizerTeamID: homeTeam,
		VenueCost:       &competition.VenueCost{Amount: 28000, Currency: "CLP"},
	}, nil)
	sr.On("Create", mock.Anything, mock.MatchedBy(func(s *settlement.Settlement) bool {
		// La mitad de $28.000, del retado hacia el organizador.
		return s.Amount == 14000 &&
			s.FromTeamID == awayTeam &&
			s.ToTeamID == homeTeam &&
			s.Source.ID == ch.CompetitionID
	})).Return(&settlement.Settlement{ID: "st-1"}, nil)

	w, c := acceptRequest(ch)
	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	sr.AssertExpectations(t)
}

// Cancha gratis no deja deuda. Es un caso normal —el amistoso en la cancha
// pública del barrio— y no algo que haya que anotar en cero.
func TestAcceptFriendly_FreeVenueOwesNothing(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	sr := &testutil.MockSettlementRepo{}
	h := newFriendlyHandler(fr, cr, mr, memr, sr)

	ch := openChallenge()
	acceptSetup(fr, cr, mr, memr, ch)
	cr.On("FindByID", mock.Anything, ch.CompetitionID).Return(&competition.Competition{
		ID:              ch.CompetitionID,
		OrganizerTeamID: homeTeam,
		VenueCost:       &competition.VenueCost{Amount: 0, Currency: "CLP"},
	}, nil)

	w, c := acceptRequest(ch)
	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	sr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}
