package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/onboarding"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func newOnboardingHandler(
	repo *testutil.MockOnboardingRepo,
	teams *testutil.MockTeamRepo,
	members *testutil.MockMembershipRepo,
) *handler.OnboardingHandler {
	return handler.NewOnboardingHandler(repo, teams, members, nil)
}

// Una invitación la responde la persona invitada, nunca el equipo que invitó.
// Si esto se invierte, un manager se mete solo en el equipo de cualquiera.
func TestRespondToInvitation_OnlyTheInvitedPersonCanAnswer(t *testing.T) {
	repo := &testutil.MockOnboardingRepo{}
	teams := &testutil.MockTeamRepo{}
	members := &testutil.MockMembershipRepo{}
	h := newOnboardingHandler(repo, teams, members)

	repo.On("FindInvitation", mock.Anything, "inv-1").Return(&onboarding.TeamInvitation{
		ID: "inv-1", TeamID: homeTeam, InvitedByUserID: "user-mgr",
		UserID: "user-invitado", Status: onboarding.InvitationSent,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr") // el que invitó intenta aceptar por el invitado
	c.Params = gin.Params{{Key: "invitationId", Value: "inv-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/team-invitations/inv-1/respond",
		strings.NewReader(`{"accept":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RespondToInvitation(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	repo.AssertNotCalled(t, "RespondToInvitation", mock.Anything, mock.Anything, mock.Anything)
}

// La solicitud de ingreso la responde el manager del equipo, no quien la envió.
func TestRespondToJoinRequest_RequesterCannotAcceptThemselves(t *testing.T) {
	repo := &testutil.MockOnboardingRepo{}
	teams := &testutil.MockTeamRepo{}
	members := &testutil.MockMembershipRepo{}
	h := newOnboardingHandler(repo, teams, members)

	repo.On("FindJoinRequest", mock.Anything, "req-1").Return(&onboarding.JoinRequest{
		ID: "req-1", TeamID: homeTeam, UserID: "user-postulante",
		Status: onboarding.JoinPending,
	}, nil)
	// El postulante no es miembro del equipo, así que no pasa el guard.
	members.On("FindByUserAndTeam", mock.Anything, "user-postulante", homeTeam).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-postulante")
	c.Params = gin.Params{{Key: "requestId", Value: "req-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/join-requests/req-1/respond",
		strings.NewReader(`{"accept":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RespondToJoinRequest(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	repo.AssertNotCalled(t, "RespondToJoinRequest", mock.Anything, mock.Anything, mock.Anything)
}

// Un jugador raso tampoco resuelve solicitudes: hace falta manager.
func TestRespondToJoinRequest_PlayerCannotAccept(t *testing.T) {
	repo := &testutil.MockOnboardingRepo{}
	teams := &testutil.MockTeamRepo{}
	members := &testutil.MockMembershipRepo{}
	h := newOnboardingHandler(repo, teams, members)

	repo.On("FindJoinRequest", mock.Anything, "req-1").Return(&onboarding.JoinRequest{
		ID: "req-1", TeamID: homeTeam, UserID: "user-postulante", Status: onboarding.JoinPending,
	}, nil)
	members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "requestId", Value: "req-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/join-requests/req-1/respond",
		strings.NewReader(`{"accept":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RespondToJoinRequest(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	repo.AssertNotCalled(t, "RespondToJoinRequest", mock.Anything, mock.Anything, mock.Anything)
}

// La búsqueda de personas exige método válido: no se puede pedir un listado
// abierto omitiendo el filtro.
func TestFindPerson_RequiresAValidMethod(t *testing.T) {
	repo := &testutil.MockOnboardingRepo{}
	teams := &testutil.MockTeamRepo{}
	members := &testutil.MockMembershipRepo{}
	h := newOnboardingHandler(repo, teams, members)

	for _, query := range []string{"", "?method=name&value=marco", "?method=tax_id"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		withClaims(c, "user-mgr")
		c.Request = httptest.NewRequest(http.MethodGet, "/people/lookup"+query, nil)

		h.FindPerson(c)

		assert.Equal(t, http.StatusBadRequest, w.Code, "query: %q", query)
	}
	repo.AssertNotCalled(t, "FindPerson", mock.Anything, mock.Anything, mock.Anything)
}

// Buscar equipos con menos de dos caracteres devuelve vacío en vez de medio padrón.
func TestSearchTeams_ShortQueryReturnsEmpty(t *testing.T) {
	repo := &testutil.MockOnboardingRepo{}
	teams := &testutil.MockTeamRepo{}
	members := &testutil.MockMembershipRepo{}
	h := newOnboardingHandler(repo, teams, members)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodGet, "/teams/search?q=r", nil)

	h.SearchTeams(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
	teams.AssertNotCalled(t, "SearchByName", mock.Anything, mock.Anything)
}
