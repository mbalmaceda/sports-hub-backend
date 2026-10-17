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

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

// En estos casos `homeTeam` es quien publicó la búsqueda y `awayTeam` quien la
// pide: el mismo reparto que queda en el desafío.

func openSearch() *friendly.Search {
	return &friendly.Search{
		ID:      "s-1",
		TeamID:  homeTeam,
		SportID: "football_7",
		StartAt: time.Now().Add(72 * time.Hour),
		Venue:   venue.Venue{ID: "v-1", Name: "Complejo Maipú", Lat: -33.51, Lng: -70.76},
		Status:  friendly.SearchOpen,
		// Publicada hace 4 horas: le quedan 20 de las 24.
		ExpiresAt: time.Now().Add(20 * time.Hour),
	}
}

type searchFixture struct {
	sr   *testutil.MockFriendlySearchRepo
	fr   *testutil.MockFriendlyRepo
	cr   *testutil.MockCompetitionRepo
	vr   *testutil.MockVenueRepo
	memr *testutil.MockMembershipRepo
	h    *handler.FriendlySearchHandler
}

func newSearchFixture() *searchFixture {
	f := &searchFixture{
		sr:   &testutil.MockFriendlySearchRepo{},
		fr:   &testutil.MockFriendlyRepo{},
		cr:   &testutil.MockCompetitionRepo{},
		vr:   &testutil.MockVenueRepo{},
		memr: &testutil.MockMembershipRepo{},
	}
	f.h = handler.NewFriendlySearchHandler(f.sr, f.fr, f.cr, f.vr, f.memr, nil, nil)
	return f
}

func requestSearch(userID, searchID, body string) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, userID)
	c.Params = gin.Params{{Key: "searchId", Value: searchID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendly-searches/"+searchID+"/requests",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return w, c
}

/*
La regla de fondo de "buscar rival": el que publicó es el retador y organiza,
aunque haya sido el otro el que tocó el botón. Así le toca contestar a él, y
al aceptar la mitad de la cancha se la debe el que pidió.
*/
func TestRequestSearch_PublisherIsTheChallenger(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()

	f.memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)
	f.fr.On("ListByTeam", mock.Anything, awayTeam).Return([]*friendly.Challenge{}, nil)
	f.cr.On("Create", mock.Anything, mock.MatchedBy(func(c *competition.Competition) bool {
		return c.OrganizerTeamID == homeTeam &&
			c.Status == competition.StatusDraft &&
			c.VenueID == "v-1" && c.Venue == "Complejo Maipú"
	})).Return(nil)
	f.fr.On("Create", mock.Anything,
		mock.MatchedBy(func(ch *friendly.Challenge) bool {
			return ch.ChallengerTeamID == homeTeam &&
				ch.ChallengedTeamID == awayTeam &&
				ch.SearchID == s.ID &&
				// El pedido no sobrevive a su búsqueda.
				!ch.ExpiresAt.After(s.ExpiresAt)
		}),
		mock.MatchedBy(func(p *friendly.Proposal) bool {
			// La propuesta es del que pidió: le toca contestar al publicador.
			return p.ProposedByTeamID == awayTeam && p.ProposedStartAt.Equal(s.StartAt)
		}),
	).Return(nil)

	w, c := requestSearch("user-away", s.ID, `{"team_id":"`+awayTeam+`","message":"Vamos"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	f.cr.AssertExpectations(t)
	f.fr.AssertExpectations(t)
}

func TestRequestSearch_CannotRequestYourOwn(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()

	f.memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)

	w, c := requestSearch("user-home", s.ID, `{"team_id":"`+homeTeam+`"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	f.cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Una búsqueda cuyo partido ya pasó se rechaza aunque Postgres siga diciendo
// 'open': la barrida corre con el tráfico y puede no haber pasado.
func TestRequestSearch_PastSearchIsClosed(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()
	s.StartAt = time.Now().Add(-time.Hour)
	s.ExpiresAt = s.StartAt

	f.memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)

	w, c := requestSearch("user-away", s.ID, `{"team_id":"`+awayTeam+`"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	f.cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// A las 24 horas de publicada se cierra aunque el partido siga lejos.
func TestRequestSearch_ExpiredSearchIsClosed(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()
	s.CreatedAt = time.Now().Add(-25 * time.Hour)
	s.ExpiresAt = s.CreatedAt.Add(friendly.SearchTTL)

	f.memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)

	w, c := requestSearch("user-away", s.ID, `{"team_id":"`+awayTeam+`"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	f.cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// El segundo toque no crea una competencia huérfana: devuelve el pedido que ya
// estaba.
func TestRequestSearch_SecondRequestIsRejectedBeforeCreatingAnything(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()

	f.memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)
	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)
	f.fr.On("ListByTeam", mock.Anything, awayTeam).Return([]*friendly.Challenge{{
		ID: "ch-prev", SearchID: s.ID, ChallengerTeamID: homeTeam, ChallengedTeamID: awayTeam,
		Status: friendly.StatusPending,
	}}, nil)

	w, c := requestSearch("user-away", s.ID, `{"team_id":"`+awayTeam+`"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "ch-prev")
	f.cr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Un jugador puede mirar el mapa, pero pedir es una decisión del equipo.
func TestRequestSearch_PlayerCannotRequest(t *testing.T) {
	f := newSearchFixture()
	player := managerOf(awayTeam, "user-player")
	player.Role = membership.RolePlayer
	f.memr.On("FindByUserAndTeam", mock.Anything, "user-player", awayTeam).Return(player, nil)

	w, c := requestSearch("user-player", "s-1", `{"team_id":"`+awayTeam+`"}`)
	f.h.Request(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	f.sr.AssertNotCalled(t, "FindByID", mock.Anything, mock.Anything)
}

func TestPublishSearch_UnknownVenueIsRejected(t *testing.T) {
	f := newSearchFixture()
	f.memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	f.vr.On("FindByID", mock.Anything, "v-missing").Return(nil, venue.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/friendly-searches",
		strings.NewReader(`{"sport_id":"football_7","start_at":"`+start+`","venue_id":"v-missing"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	f.h.Publish(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	f.sr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func publishSearch(f *searchFixture, startAt time.Time) *httptest.ResponseRecorder {
	f.memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	f.vr.On("FindByID", mock.Anything, "v-1").
		Return(&venue.Venue{ID: "v-1", Name: "Complejo Maipú"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	start := startAt.UTC().Format(time.RFC3339)
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/friendly-searches",
		strings.NewReader(`{"sport_id":"football_7","start_at":"`+start+`","venue_id":"v-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	f.h.Publish(c)
	return w
}

// Una búsqueda vive 24 horas desde que se publica, aunque el partido sea en
// cinco días.
func TestPublishSearch_ExpiresIn24Hours(t *testing.T) {
	f := newSearchFixture()
	before := time.Now()
	f.sr.On("Create", mock.Anything, mock.MatchedBy(func(s *friendly.Search) bool {
		return !s.ExpiresAt.Before(before.Add(friendly.SearchTTL)) &&
			!s.ExpiresAt.After(time.Now().Add(friendly.SearchTTL))
	})).Return(nil)

	w := publishSearch(f, before.Add(5*24*time.Hour))

	assert.Equal(t, http.StatusCreated, w.Code)
	f.sr.AssertExpectations(t)
}

// Y nunca más allá del partido: uno en 3 horas vence en 3 horas.
func TestPublishSearch_ExpiryNeverOutlivesTheMatch(t *testing.T) {
	f := newSearchFixture()
	start := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	f.sr.On("Create", mock.Anything, mock.MatchedBy(func(s *friendly.Search) bool {
		return s.ExpiresAt.Equal(start)
	})).Return(nil)

	w := publishSearch(f, start)

	assert.Equal(t, http.StatusCreated, w.Code)
	f.sr.AssertExpectations(t)
}

// Bajar la búsqueda rechaza los pedidos que esperaban y cancela sus
// competencias: si no, quedan como borradores en la bandeja del publicador.
func TestCancelSearch_ReleasesPendingRequests(t *testing.T) {
	f := newSearchFixture()
	s := openSearch()

	f.sr.On("FindByID", mock.Anything, s.ID).Return(s, nil)
	f.memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	f.sr.On("Close", mock.Anything, s.ID, friendly.SearchCancelled, "").Return([]*friendly.Challenge{
		{ID: "ch-2", CompetitionID: "comp-2", ChallengerTeamID: homeTeam, ChallengedTeamID: awayTeam},
	}, nil)
	f.cr.On("UpdateStatus", mock.Anything, "comp-2", competition.StatusCancelled).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "searchId", Value: s.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendly-searches/"+s.ID+"/cancel", nil)

	f.h.Cancel(c)

	assert.Equal(t, http.StatusOK, w.Code)
	f.cr.AssertExpectations(t)
}

// ─── Aceptar un pedido ────────────────────────────────────────────────────────

func searchAcceptHandler(
	fr *testutil.MockFriendlyRepo,
	sr *testutil.MockFriendlySearchRepo,
	cr *testutil.MockCompetitionRepo,
	mr *testutil.MockMatchRepo,
	memr *testutil.MockMembershipRepo,
) *handler.FriendlyHandler {
	return handler.NewFriendlyHandler(fr, sr, cr, mr, memr, &testutil.MockSettlementRepo{}, nil, nil)
}

func requestedChallenge() *friendly.Challenge {
	ch := openChallenge()
	ch.SearchID = "s-1"
	return ch
}

// El publicador acepta un pedido: la búsqueda queda cerrada y los demás pedidos
// rechazados, con sus competencias canceladas.
func TestAcceptRequest_ClosesSearchAndDeclinesTheRest(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	sr := &testutil.MockFriendlySearchRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := searchAcceptHandler(fr, sr, cr, mr, memr)

	ch := requestedChallenge()
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: awayTeam,
		ProposedStartAt: time.Now().Add(72 * time.Hour), ProposedVenue: "Complejo Maipú",
	}, nil)
	sr.On("Close", mock.Anything, "s-1", friendly.SearchMatched, ch.ID).Return([]*friendly.Challenge{
		{ID: "ch-other", CompetitionID: "comp-other", ChallengerTeamID: homeTeam, ChallengedTeamID: "team-third"},
	}, nil)
	fr.On("UpdateStatus", mock.Anything, ch.ID, friendly.StatusAccepted).Return(nil)
	mr.On("Create", mock.Anything, mock.Anything).Return(nil)
	cr.On("UpdateSchedule", mock.Anything, ch.CompetitionID, mock.Anything, mock.Anything).Return(nil)
	cr.On("UpsertEntry", mock.Anything, mock.Anything).Return(nil).Twice()
	cr.On("UpdateStatus", mock.Anything, ch.CompetitionID, competition.StatusActive).Return(nil)
	cr.On("FindByID", mock.Anything, ch.CompetitionID).Return(&competition.Competition{ID: ch.CompetitionID}, nil)
	cr.On("UpdateStatus", mock.Anything, "comp-other", competition.StatusCancelled).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	sr.AssertExpectations(t)
	cr.AssertCalled(t, "UpdateStatus", mock.Anything, "comp-other", competition.StatusCancelled)
}

// Si otro pedido ya se aceptó, este no llega a tener partido: el cierre de la
// búsqueda es el candado.
func TestAcceptRequest_SearchAlreadyMatchedIsRejected(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	sr := &testutil.MockFriendlySearchRepo{}
	cr := &testutil.MockCompetitionRepo{}
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	h := searchAcceptHandler(fr, sr, cr, mr, memr)

	ch := requestedChallenge()
	fr.On("FindByID", mock.Anything, ch.ID).Return(ch, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	fr.On("LatestProposal", mock.Anything, ch.ID).Return(&friendly.Proposal{
		ID: "p-1", ChallengeID: ch.ID, ProposedByTeamID: awayTeam,
		ProposedStartAt: time.Now().Add(72 * time.Hour),
	}, nil)
	sr.On("Close", mock.Anything, "s-1", friendly.SearchMatched, ch.ID).Return(nil, friendly.ErrSearchClosed)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "challengeId", Value: ch.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/friendlies/"+ch.ID+"/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	mr.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	fr.AssertNotCalled(t, "UpdateStatus", mock.Anything, mock.Anything, mock.Anything)
}
