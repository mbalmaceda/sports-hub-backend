package handler_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

type guestDeps struct {
	invites      *testutil.MockGuestInviteRepo
	matches      *testutil.MockMatchRepo
	memberships  *testutil.MockMembershipRepo
	competitions *testutil.MockCompetitionRepo
	charges      *testutil.MockChargeRepo
	teams        *testutil.MockTeamRepo
	users        *testutil.MockUserRepo
}

func newGuestHandler() (*handler.GuestHandler, *guestDeps) {
	d := &guestDeps{
		invites:      &testutil.MockGuestInviteRepo{},
		matches:      &testutil.MockMatchRepo{},
		memberships:  &testutil.MockMembershipRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		charges:      &testutil.MockChargeRepo{},
		teams:        &testutil.MockTeamRepo{},
		users:        &testutil.MockUserRepo{},
	}
	h := handler.NewGuestHandler(
		d.invites, d.matches, d.memberships, d.competitions, d.charges, d.teams, d.users, nil,
		config.Config{PublicBaseURL: "https://zports.test"})
	return h, d
}

func hashFor(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func openInvite() *guest.Invite {
	return &guest.Invite{
		ID:              "invite-1",
		MatchID:         "match-1",
		TeamID:          homeTeam,
		CreatedByUserID: "user-home",
		MaxUses:         3,
		UsedCount:       0,
		ExpiresAt:       time.Now().Add(24 * time.Hour),
	}
}

// ─── Crear el enlace ─────────────────────────────────────────────────────────

func TestCreateGuestInvite_Success(t *testing.T) {
	h, d := newGuestHandler()

	m := confirmedMatch()
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	d.invites.On("Create", mock.Anything, mock.AnythingOfType("*guest.Invite"), mock.AnythingOfType("string")).
		Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":3}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// El token en claro se ve una sola vez, acá.
	assert.NotEmpty(t, body["token"])
	assert.Equal(t, float64(3), body["max_uses"])
}

// Sumar gente al partido es convocar: no lo hace cualquiera del plantel.
func TestCreateGuestInvite_RejectsNonManager(t *testing.T) {
	h, d := newGuestHandler()

	m := confirmedMatch()
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(&membership.Membership{
			ID: "m-player", UserID: "user-player", TeamID: homeTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":2}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.invites.AssertNotCalled(t, "Create")
}

// El cupo lo propone el cliente, pero el servidor lo acota: un max_uses enorme
// convertiría el enlace en una puerta abierta al equipo.
func TestCreateGuestInvite_CapsMaxUses(t *testing.T) {
	h, d := newGuestHandler()

	m := confirmedMatch()
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":500}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	d.invites.AssertNotCalled(t, "Create")
}

// Un enlace que nace vencido es un botón que no hace nada.
func TestCreateGuestInvite_RejectsMatchAlreadyStarted(t *testing.T) {
	h, d := newGuestHandler()

	m := confirmedMatch()
	m.ScheduledAt = time.Now().Add(-1 * time.Hour)
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":2}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	d.invites.AssertNotCalled(t, "Create")
}

// ─── La vista previa pública ─────────────────────────────────────────────────

func TestGetInvite_ShowsMatchAndCostOnly(t *testing.T) {
	h, d := newGuestHandler()

	inv := openInvite()
	share := int64(2000)
	d.invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(inv, nil)
	d.matches.On("FindByID", mock.Anything, inv.MatchID).Return(confirmedMatch(), nil)
	d.teams.On("FindByID", mock.Anything, homeTeam).
		Return(&team.Team{ID: homeTeam, Name: "Deportivo Norte"}, nil)
	d.teams.On("FindByID", mock.Anything, awayTeam).
		Return(&team.Team{ID: awayTeam, Name: "Stars FC"}, nil)
	d.users.On("FindByID", mock.Anything, "user-home").
		Return(&user.User{ID: "user-home", Name: "Mirko", Email: "mirko@example.com", Phone: "+56900000000"}, nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(&competition.Competition{
		ID:          "comp-1",
		PlayerShare: &share,
		VenueCost:   &competition.VenueCost{Amount: 28000, Currency: "CLP"},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/invites/tok", nil)

	h.GetInvite(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Deportivo Norte", body["team_name"])
	assert.Equal(t, "Mirko", body["invited_by"])
	assert.Equal(t, "Stars FC", body["opponent_name"])
	// El costo va sí o sí: enterarse en la cancha de que se debe la cuota es
	// exactamente lo que esta pantalla evita.
	assert.Equal(t, float64(2000), body["cost_per_player"])
	assert.Equal(t, float64(3), body["remaining_uses"])

	// Es una URL pública: no puede filtrar datos de nadie.
	assert.NotContains(t, w.Body.String(), "mirko@example.com")
	assert.NotContains(t, w.Body.String(), "+56900000000")
	assert.NotContains(t, w.Body.String(), "user-home")
}

// Vencido, revocado o sin cupo son el mismo 410: al que llegó tarde hay que
// decirle que llegó tarde, y a quien esté probando tokens no hay que contarle
// nada más.
func TestGetInvite_GoneWhenNoSlotsLeft(t *testing.T) {
	h, d := newGuestHandler()

	inv := openInvite()
	inv.UsedCount = inv.MaxUses
	d.invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(inv, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/invites/tok", nil)

	h.GetInvite(c)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetInvite_GoneWhenExpired(t *testing.T) {
	h, d := newGuestHandler()

	inv := openInvite()
	inv.ExpiresAt = time.Now().Add(-time.Minute)
	d.invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(inv, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/invites/tok", nil)

	h.GetInvite(c)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetInvite_GoneWhenRevoked(t *testing.T) {
	h, d := newGuestHandler()

	inv := openInvite()
	revoked := time.Now().Add(-time.Hour)
	inv.RevokedAt = &revoked
	d.invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(inv, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/invites/tok", nil)

	h.GetInvite(c)

	assert.Equal(t, http.StatusGone, w.Code)
}

// El token viaja en claro por la URL pero en la base solo vive su hash: lo que
// se busca nunca es lo que se recibió.
func TestGetInvite_LooksUpByHashNotByToken(t *testing.T) {
	h, d := newGuestHandler()

	d.invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(nil, guest.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/invites/tok", nil)

	h.GetInvite(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	d.invites.AssertCalled(t, "FindByTokenHash", mock.Anything, hashFor("tok"))
	d.invites.AssertNotCalled(t, "FindByTokenHash", mock.Anything, "tok")
}

// ─── El canje ────────────────────────────────────────────────────────────────

func TestAcceptInvite_CreatesChargeForTheGuest(t *testing.T) {
	h, d := newGuestHandler()

	share := int64(2000)
	d.invites.On("Redeem", mock.Anything, hashFor("tok"), "nuevo-user").Return(&guest.AcceptResult{
		MembershipID: "m-guest",
		TeamID:       homeTeam,
		MatchID:      "match-1",
	}, nil)
	d.matches.On("FindByID", mock.Anything, "match-1").Return(confirmedMatch(), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(&competition.Competition{
		ID:          "comp-1",
		PlayerShare: &share,
		VenueCost:   &competition.VenueCost{Amount: 28000, Currency: "CLP"},
	}, nil)
	d.charges.On("EnsureForMembership", mock.Anything, mock.Anything).Return(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "nuevo-user")
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/invites/tok/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// El parche paga la cancha por la app como cualquiera: el cargo nace con el
	// canje, no cuando el manager se acuerda de rehacer el reparto.
	d.charges.AssertCalled(t, "EnsureForMembership", mock.Anything, mock.Anything)
}

func TestAcceptInvite_GoneWhenNoSlotsLeft(t *testing.T) {
	h, d := newGuestHandler()

	d.invites.On("Redeem", mock.Anything, hashFor("tok"), "nuevo-user").
		Return(nil, guest.ErrNotUsable)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "nuevo-user")
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/invites/tok/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusGone, w.Code)
	d.charges.AssertNotCalled(t, "EnsureForMembership")
}

// El del plantel que abre el enlace del grupo no gasta un lugar de nadie.
func TestAcceptInvite_ConflictWhenAlreadyMember(t *testing.T) {
	h, d := newGuestHandler()

	d.invites.On("Redeem", mock.Anything, hashFor("tok"), "ya-esta").
		Return(nil, guest.ErrAlreadyMember)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "ya-esta")
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/invites/tok/accept", nil)

	h.Accept(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// ─── Revocar ─────────────────────────────────────────────────────────────────

func TestRevokeInvite_RejectsNonManager(t *testing.T) {
	h, d := newGuestHandler()

	d.invites.On("FindByID", mock.Anything, "invite-1").Return(openInvite(), nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(&membership.Membership{
			ID: "m-player", UserID: "user-player", TeamID: homeTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "inviteId", Value: "invite-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/guest-invites/invite-1", nil)

	h.RevokeInvite(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.invites.AssertNotCalled(t, "Revoke")
}

func TestRevokeInvite_Success(t *testing.T) {
	h, d := newGuestHandler()

	d.invites.On("FindByID", mock.Anything, "invite-1").Return(openInvite(), nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	d.invites.On("Revoke", mock.Anything, "invite-1").Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "inviteId", Value: "invite-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/guest-invites/invite-1", nil)

	h.RevokeInvite(c)

	assert.Equal(t, http.StatusOK, w.Code)
	d.invites.AssertExpectations(t)
}

// ─── El alcance del invitado ─────────────────────────────────────────────────

// Un invitado tiene membresía para que la convocatoria y el cobro funcionen,
// pero no es del equipo: el plantel no se le muestra.
func TestGuestCannotListRoster(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.ListByTeam(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	repo.AssertNotCalled(t, "ListByTeam")
}

// Su partido sí lo ve: la llave es la convocatoria, no la membresía.
func TestGuestCanSeeTheMatchTheyWereCalledTo(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	mr.On("ListCallups", mock.Anything, m.ID).Return([]*match.Callup{
		{ID: "cu-1", MatchID: m.ID, MembershipID: "m-guest", Status: match.CallupConfirmed},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.ListCallups(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// Otro partido del mismo equipo, no. Es la diferencia entre entrar a un partido
// y entrar al club.
func TestGuestCannotSeeAnotherMatchOfTheSameTeam(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	other := confirmedMatch()
	other.ID = "match-otro"
	mr.On("FindByID", mock.Anything, other.ID).Return(other, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", mock.Anything).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	// Convocado al otro partido, no a este.
	mr.On("ListCallups", mock.Anything, other.ID).Return([]*match.Callup{
		{ID: "cu-9", MatchID: other.ID, MembershipID: "m-otro", Status: match.CallupConfirmed},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "matchId", Value: other.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.ListCallups(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// Un parche del mes pasado no se recita desde la nómina: su vínculo se apagó
// con el partido por el que entró. Para volver a sumarlo va un enlace nuevo.
func TestCallUp_RejectsPastGuests(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	memr.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-1", TeamID: homeTeam, Status: membership.StatusActive},
		{
			MembershipID: "m-guest", TeamID: homeTeam,
			Kind: membership.KindGuest, Status: membership.StatusActive,
		},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","membership_ids":["m-guest"]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CallUp(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mr.AssertNotCalled(t, "CallUp", mock.Anything, mock.Anything, mock.Anything)
}

// ─── El enlace compartible ───────────────────────────────────────────────────

// La URL la arma el servidor, y es https. El cliente no sabe con qué dominio se
// sirven las invitaciones, y el que tiene a mano —`zports://`— es justo el que
// no sirve: quien la recibe no tiene la app, así que en WhatsApp no abre nada.
func TestCreateGuestInvite_ReturnsShareableHttpsURL(t *testing.T) {
	h, d := newGuestHandler()

	m := confirmedMatch()
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	d.invites.On("Create", mock.Anything, mock.AnythingOfType("*guest.Invite"), mock.AnythingOfType("string")).
		Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":2}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	url, _ := body["url"].(string)
	assert.True(t, strings.HasPrefix(url, "https://zports.test/i/"), "url fue %q", url)
	assert.NotContains(t, url, "zports://")
}

// Sin PUBLIC_BASE_URL no hay enlace que compartir. Va vacío y no roto: mandar
// una URL inválida por WhatsApp se paga con la persona que la recibió.
func TestCreateGuestInvite_NoURLWhenPublicBaseIsUnset(t *testing.T) {
	d := &guestDeps{
		invites:      &testutil.MockGuestInviteRepo{},
		matches:      &testutil.MockMatchRepo{},
		memberships:  &testutil.MockMembershipRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		charges:      &testutil.MockChargeRepo{},
		teams:        &testutil.MockTeamRepo{},
		users:        &testutil.MockUserRepo{},
	}
	h := handler.NewGuestHandler(
		d.invites, d.matches, d.memberships, d.competitions, d.charges, d.teams, d.users, nil,
		config.Config{})

	m := confirmedMatch()
	d.matches.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	d.memberships.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	d.invites.On("Create", mock.Anything, mock.AnythingOfType("*guest.Invite"), mock.AnythingOfType("string")).
		Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"team_id":"`+homeTeam+`","max_uses":2}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateInvite(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "", body["url"])
}

// ─── Sumar al plantel ────────────────────────────────────────────────────────

func TestPromoteGuest_Success(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	guestMember := &membership.TeamMember{
		MembershipID: "m-guest", TeamID: homeTeam,
		Kind: membership.KindGuest, Status: membership.StatusActive,
	}
	repo.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	repo.On("GetMemberByID", mock.Anything, "m-guest").Return(guestMember, nil)
	repo.On("PromoteGuest", mock.Anything, "m-guest").Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}, {Key: "membershipId", Value: "m-guest"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	h.PromoteGuest(c)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertCalled(t, "PromoteGuest", mock.Anything, "m-guest")
}

// Sumar a alguien al plantel le empieza a generar cuota mensual: no lo hace
// cualquiera del equipo.
func TestPromoteGuest_RejectsNonManager(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	memberOf(repo, homeTeam, "user-player")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}, {Key: "membershipId", Value: "m-guest"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	h.PromoteGuest(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	repo.AssertNotCalled(t, "PromoteGuest", mock.Anything, mock.Anything)
}

// El manager de un equipo no asciende a un invitado de otro.
func TestPromoteGuest_RejectsMemberFromAnotherTeam(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	repo.On("GetMemberByID", mock.Anything, "m-ajeno").Return(&membership.TeamMember{
		MembershipID: "m-ajeno", TeamID: awayTeam,
		Kind: membership.KindGuest, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}, {Key: "membershipId", Value: "m-ajeno"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	h.PromoteGuest(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertNotCalled(t, "PromoteGuest", mock.Anything, mock.Anything)
}

func TestPromoteGuest_ConflictWhenAlreadySquad(t *testing.T) {
	repo := &testutil.MockMembershipRepo{}
	h := newRosterHandler(repo)

	repo.On("FindByUserAndTeam", mock.Anything, "user-home", homeTeam).
		Return(managerOf(homeTeam, "user-home"), nil)
	repo.On("GetMemberByID", mock.Anything, "m-1").Return(&membership.TeamMember{
		MembershipID: "m-1", TeamID: homeTeam,
		Kind: membership.KindMember, Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-home")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}, {Key: "membershipId", Value: "m-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	h.PromoteGuest(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	repo.AssertNotCalled(t, "PromoteGuest", mock.Anything, mock.Anything)
}

// ─── App Links ───────────────────────────────────────────────────────────────

// Sin fingerprints, 404 y no un array vacío: un assetlinks vacío le dice a
// Android "acá no hay ninguna app asociada" y queda cacheado así.
func TestAssetLinks_NotFoundWhenUnconfigured(t *testing.T) {
	h := handler.NewAppLinksHandler(&testutil.MockGuestInviteRepo{}, config.Config{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil)

	h.AssetLinks(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAssetLinks_ServesConfiguredFingerprints(t *testing.T) {
	h := handler.NewAppLinksHandler(&testutil.MockGuestInviteRepo{}, config.Config{
		AndroidPackageName:      "com.zports.app",
		AndroidCertFingerprints: []string{"AA:BB:CC"},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil)

	h.AssetLinks(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "com.zports.app")
	assert.Contains(t, w.Body.String(), "AA:BB:CC")
	assert.Contains(t, w.Body.String(), "delegate_permission/common.handle_all_urls")
}

// El token viene de la URL y termina adentro de un href: tiene que salir
// escapado, o el enlace es una inyección de HTML servida desde el dominio propio.
func TestInvitePage_EscapesTheTokenInHTML(t *testing.T) {
	invites := &testutil.MockGuestInviteRepo{}
	h := handler.NewAppLinksHandler(invites, config.Config{PlayStoreURL: "https://play.example"})

	nasty := `"><script>alert(1)</script>`
	invites.On("FindByTokenHash", mock.Anything, hashFor(nasty)).Return(openInvite(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: nasty}}
	c.Request = httptest.NewRequest(http.MethodGet, "/i/x", nil)

	h.InvitePage(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "<script>alert(1)</script>")
}

func TestInvitePage_GoneWhenInviteIsUsedUp(t *testing.T) {
	invites := &testutil.MockGuestInviteRepo{}
	h := handler.NewAppLinksHandler(invites, config.Config{})

	inv := openInvite()
	inv.UsedCount = inv.MaxUses
	invites.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(inv, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/i/tok", nil)

	h.InvitePage(c)

	assert.Equal(t, http.StatusGone, w.Code)
	assert.Contains(t, w.Body.String(), "ya no sirve")
}

// El botón "Ya tengo la app" tiene que abrir la app de verdad.
//
// html/template sanea los href y solo deja pasar esquemas conocidos: `zports://`
// salía como "#ZgotmplZ" y el botón quedaba muerto. Es el respaldo de App Links
// —lo que salva al que ya tiene la app cuando la verificación no está
// configurada— así que muerto no sirve de nada.
func TestInvitePage_AppSchemeSurvivesTemplateEscaping(t *testing.T) {
	invites := &testutil.MockGuestInviteRepo{}
	h := handler.NewAppLinksHandler(invites, config.Config{})

	invites.On("FindByTokenHash", mock.Anything, hashFor("aX9tok")).Return(openInvite(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: "aX9tok"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/i/aX9tok", nil)

	h.InvitePage(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "zports://invite/aX9tok")
	assert.NotContains(t, w.Body.String(), "ZgotmplZ")
}

// El partido lo lee quien lo juega. Sin esta guarda alcanzaba con tener sesión y
// el id para leer el partido de cualquier club.
func TestGetMatch_RejectsSomeoneFromNeitherTeam(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "forastero", mock.Anything).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "forastero")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// Pero el invitado citado sí lo lee: es el partido al que lo invitaron.
func TestGetMatch_AllowsTheGuestOfThatMatch(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	m := confirmedMatch()
	mr.On("FindByID", mock.Anything, m.ID).Return(m, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	mr.On("ListCallups", mock.Anything, m.ID).Return([]*match.Callup{
		{ID: "cu-1", MatchID: m.ID, MembershipID: "m-guest", Status: match.CallupConfirmed},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "matchId", Value: m.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── Acceso a la competencia ─────────────────────────────────────────────────

func competitionOf(teamID string) *competition.Competition {
	return &competition.Competition{
		ID: "comp-1", Type: competition.TypeFriendly, OrganizerTeamID: teamID,
	}
}

func newCompetitionHandler() (*handler.CompetitionHandler, *testutil.MockCompetitionRepo, *testutil.MockMembershipRepo, *testutil.MockMatchRepo) {
	cr := &testutil.MockCompetitionRepo{}
	memr := &testutil.MockMembershipRepo{}
	mr := &testutil.MockMatchRepo{}
	return handler.NewCompetitionHandler(cr, memr, mr), cr, memr, mr
}

// Antes alcanzaba con tener sesión y el UUID para leer la competencia de
// cualquier club: su fecha, su cancha y cuánto cuesta.
func TestGetCompetition_RejectsSomeoneWhoDoesNotPlayIt(t *testing.T) {
	h, cr, memr, _ := newCompetitionHandler()

	cr.On("FindByID", mock.Anything, "comp-1").Return(competitionOf(homeTeam), nil)
	cr.On("ListEntries", mock.Anything, "comp-1").Return([]*competition.Entry{
		{CompetitionID: "comp-1", TeamID: homeTeam},
		{CompetitionID: "comp-1", TeamID: awayTeam},
	}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "forastero", mock.Anything).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "forastero")
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// El rival de un amistoso la lee aunque no la haya organizado: está en las
// entradas, y para eso se consultan.
func TestGetCompetition_AllowsTheRivalTeam(t *testing.T) {
	h, cr, memr, _ := newCompetitionHandler()

	cr.On("FindByID", mock.Anything, "comp-1").Return(competitionOf(homeTeam), nil)
	cr.On("ListEntries", mock.Anything, "comp-1").Return([]*competition.Entry{
		{CompetitionID: "comp-1", TeamID: awayTeam},
	}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", homeTeam).
		Return(nil, membership.ErrNotFound)
	memr.On("FindByUserAndTeam", mock.Anything, "user-away", awayTeam).
		Return(managerOf(awayTeam, "user-away"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-away")
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// El invitado la lee porque la cuota por jugador sale de acá: sin esto no puede
// saber cuánto debe. Su llave es la convocatoria, igual que en el partido.
func TestGetCompetition_AllowsAGuestCalledToOneOfItsMatches(t *testing.T) {
	h, cr, memr, mr := newCompetitionHandler()

	cr.On("FindByID", mock.Anything, "comp-1").Return(competitionOf(homeTeam), nil)
	cr.On("ListEntries", mock.Anything, "comp-1").Return([]*competition.Entry{}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	mr.On("ListCallupsByMembership", mock.Anything, "m-guest").Return([]*match.Callup{
		{ID: "cu-1", MatchID: "match-1", MembershipID: "m-guest"},
	}, nil)
	mr.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1"},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// Pero no otra competencia del mismo equipo: su membresía sola no abre nada.
func TestGetCompetition_RejectsAGuestFromAnotherCompetition(t *testing.T) {
	h, cr, memr, mr := newCompetitionHandler()

	cr.On("FindByID", mock.Anything, "comp-1").Return(competitionOf(homeTeam), nil)
	cr.On("ListEntries", mock.Anything, "comp-1").Return([]*competition.Entry{}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	// Su convocatoria es a un partido de OTRA competencia.
	mr.On("ListCallupsByMembership", mock.Anything, "m-guest").Return([]*match.Callup{
		{ID: "cu-9", MatchID: "match-otro", MembershipID: "m-guest"},
	}, nil)
	mr.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1"},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetByID(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

/*
Sus propias convocatorias sí: es lo que la app le muestra en el inicio.

Antes esto pedía `requireMember`, que deja afuera a los invitados, y el parche
que acababa de confirmar no veía en ninguna parte el partido al que iba: el
inicio le quedaba vacío. El corte sigue estando, pero ahora es el correcto —lo
suyo sí, el historial de los demás no.
*/
func TestGuestCanListTheirOwnCallups(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	memr.On("GetMemberByID", mock.Anything, "m-guest").Return(&membership.TeamMember{
		MembershipID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)
	mr.On("ListCallupsByMembership", mock.Anything, "m-guest").Return([]*match.Callup{
		{ID: "cu-1", MatchID: "match-1", MembershipID: "m-guest", Status: match.CallupConfirmed},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "membershipId", Value: "m-guest"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.ListCallupsByMembership(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// Las de un compañero, no: el historial de asistencia es información de
// plantel, y el invitado no es del plantel.
func TestGuestCannotListSomeoneElsesCallups(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	memr := &testutil.MockMembershipRepo{}
	compr := &testutil.MockCompetitionRepo{}
	chr := &testutil.MockChargeRepo{}
	h := handler.NewMatchHandler(mr, memr, compr, chr, nil, nil)

	memr.On("GetMemberByID", mock.Anything, "m-titular").Return(&membership.TeamMember{
		MembershipID: "m-titular", UserID: "titular", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)
	memr.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).Return(&membership.Membership{
		ID: "m-guest", UserID: "parche", TeamID: homeTeam,
		Role: membership.RolePlayer, Kind: membership.KindGuest,
		Status: membership.StatusActive,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "membershipId", Value: "m-titular"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.ListCallupsByMembership(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mr.AssertNotCalled(t, "ListCallupsByMembership")
}
