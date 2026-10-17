package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
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
	c := &competition.Competition{
		ID: "comp-1", Type: competition.TypeFriendly, OrganizerTeamID: homeTeam,
		PlayersPerSide: &perSide,
		VenueCost:      &competition.VenueCost{Amount: 28000, Currency: "CLP"},
	}
	// Igual que en la base: la cuota queda resuelta al crearse, no en cada
	// lectura. $28.000 entre 14 son $2.000.
	c.PlayerShare = c.ResolvePlayerShare()
	return c
}

func remindRequest(c *gin.Context) {
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/"+homeTeam+"/charge-reminders",
		strings.NewReader(`{"source_type":"match_cost","source_id":"comp-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")
}

// reminderHistory hace de tabla de notificaciones con la clave de deduplicación
// incluida: el límite de uno por día es justo lo que se prueba, y un doble sin
// ella no probaría nada.
type reminderHistory struct {
	mu     sync.Mutex
	drafts []notification.Draft
	keys   map[string]bool
}

func (h *reminderHistory) CreateMany(
	_ context.Context, drafts []notification.Draft,
) ([]*notification.Notification, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*notification.Notification, 0, len(drafts))
	for _, d := range drafts {
		key := d.RecipientUserID + "|" + d.DedupeKey
		if d.DedupeKey != "" && h.keys[key] && d.OnConflict == notification.ConflictSkip {
			continue
		}
		if d.DedupeKey != "" {
			h.keys[key] = true
		}
		h.drafts = append(h.drafts, d)
		out = append(out, &notification.Notification{
			ID: "n-" + d.RecipientUserID, RecipientUserID: d.RecipientUserID,
			Type: d.Type, EntityID: d.EntityID,
		})
	}
	return out, nil
}
func (h *reminderHistory) ListByUser(context.Context, string, int) (*notification.Feed, error) {
	return nil, nil
}
func (h *reminderHistory) MarkRead(context.Context, string, string, time.Time) error { return nil }
func (h *reminderHistory) MarkAllRead(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

// Sin teléfonos registrados no hay push que esperar: lo que se prueba es la fila.
type noDevices struct{}

func (noDevices) Register(context.Context, string, string, time.Time) error { return nil }
func (noDevices) DevicesByUserIDs(context.Context, []string) ([]notification.Device, error) {
	return nil, nil
}

func newRemindingChargeHandler() (*handler.ChargeHandler, chargeDeps, *reminderHistory) {
	d := chargeDeps{
		charges:      &testutil.MockChargeRepo{},
		competitions: &testutil.MockCompetitionRepo{},
		matches:      &testutil.MockMatchRepo{},
		members:      &testutil.MockMembershipRepo{},
		funds:        &testutil.MockFundsRepo{},
	}
	history := &reminderHistory{keys: map[string]bool{}}
	svc := notify.NewService(nil, history, noDevices{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return handler.NewChargeHandler(d.charges, d.competitions, d.matches, d.members, d.funds, svc), d, history
}

// expectEnsure devuelve el cobro de esa membresía con el estado que ya tenía.
func expectEnsure(d chargeDeps, membershipID string, status charge.Status) {
	d.charges.On("EnsureForMembership", mock.Anything, mock.MatchedBy(func(in charge.EnsureInput) bool {
		return in.MembershipID == membershipID
	})).Return(&charge.Charge{
		ID: "charge-" + membershipID, TeamID: homeTeam, MembershipID: membershipID,
		Amount: 2000, Currency: "CLP", Status: status,
	}, nil)
}

// El partido con un plantel de cinco, uno por cada caso que importa.
func stubFriendlyWithCallups(d chargeDeps) {
	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{
		{MembershipID: "m-debe", Status: match.CallupConfirmed},
		{MembershipID: "m-pago", Status: match.CallupConfirmed},
		{MembershipID: "m-citado", Status: match.CallupCalled},
		{MembershipID: "m-no-va", Status: match.CallupDeclined},
		{MembershipID: "m-del-rival", Status: match.CallupConfirmed},
	}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-debe", UserID: "user-debe", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-pago", UserID: "user-pago", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-citado", UserID: "user-citado", TeamID: homeTeam, Status: membership.StatusActive},
		{MembershipID: "m-no-va", UserID: "user-no-va", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)
	expectEnsure(d, "m-debe", charge.StatusPending)
	expectEnsure(d, "m-pago", charge.StatusPaid)
	d.charges.On("ListBySource", mock.Anything, charge.Source{Type: charge.SourceMatchCost, ID: "comp-1"}).
		Return([]*charge.Charge{
			{ID: "charge-m-debe", TeamID: homeTeam, MembershipID: "m-debe", Status: charge.StatusPending},
			{ID: "charge-m-pago", TeamID: homeTeam, MembershipID: "m-pago", Status: charge.StatusPaid},
			{ID: "charge-rival", TeamID: awayTeam, MembershipID: "m-del-rival", Status: charge.StatusPending},
		}, nil)
}

type remindResponse struct {
	Notified int              `json:"notified"`
	Pending  int              `json:"pending"`
	Charges  []*charge.Charge `json:"charges"`
}

func remind(t *testing.T, h *handler.ChargeHandler, userID string) (int, remindResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, userID)
	remindRequest(c)
	h.RemindPending(c)

	var body remindResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	}
	return w.Code, body
}

// La regla entera: un cobro existe si y solo si confirmaste, y el aviso va a
// quien lo debe. El citado que no contestó no recibe cobro ni aviso, el que
// pagó tampoco, y la convocatoria del rival no se toca.
func TestRemindPending_OnlyConfirmedWhoStillOwe(t *testing.T) {
	h, d, history := newRemindingChargeHandler()
	stubFriendlyWithCallups(d)

	code, body := remind(t, h, "user-mgr")

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, body.Notified)
	assert.Equal(t, 1, body.Pending)

	require.Len(t, history.drafts, 1)
	draft := history.drafts[0]
	assert.Equal(t, "user-debe", draft.RecipientUserID)
	assert.Equal(t, notification.TypeChargeReminder, draft.Type)
	// Cada uno a SU cobro, no al partido.
	assert.Equal(t, "charge-m-debe", draft.EntityID)
	assert.True(t, strings.HasPrefix(draft.DedupeKey, "charge_reminder:comp-1:"))
	assert.Equal(t, notification.ConflictSkip, draft.OnConflict)

	// Solo se aseguró el cobro de los confirmados propios.
	d.charges.AssertNumberOfCalls(t, "EnsureForMembership", 2)
	for _, ch := range body.Charges {
		assert.Equal(t, homeTeam, ch.TeamID, "los cobros del rival no vuelven")
	}
}

// Uno por día: el segundo toque no escribe fila ni manda push, y la respuesta
// lo dice en vez de anunciar un aviso que no salió.
func TestRemindPending_OncePerDay(t *testing.T) {
	h, d, history := newRemindingChargeHandler()
	stubFriendlyWithCallups(d)

	_, first := remind(t, h, "user-mgr")
	code, second := remind(t, h, "user-mgr")

	assert.Equal(t, 1, first.Notified)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 0, second.Notified)
	assert.Equal(t, 1, second.Pending, "sigue debiendo: ya se le avisó hoy")
	assert.Len(t, history.drafts, 1)
}

// El manager que juega y debe su parte no se recuerda a sí mismo: es quien
// está tocando el botón.
func TestRemindPending_DoesNotNotifyTheCaller(t *testing.T) {
	h, d, history := newRemindingChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusConfirmed},
	}, nil)
	d.matches.On("ListCallups", mock.Anything, "match-1").Return([]*match.Callup{
		{MembershipID: "m-mgr", Status: match.CallupConfirmed},
	}, nil)
	d.members.On("ListByTeam", mock.Anything, homeTeam).Return([]*membership.TeamMember{
		{MembershipID: "m-mgr", UserID: "user-mgr", TeamID: homeTeam, Status: membership.StatusActive},
	}, nil)
	expectEnsure(d, "m-mgr", charge.StatusPending)
	d.charges.On("ListBySource", mock.Anything, mock.Anything).Return([]*charge.Charge{}, nil)

	code, body := remind(t, h, "user-mgr")

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 0, body.Notified)
	assert.Empty(t, history.drafts)
}

// Un partido cancelado no se cobra: lo que queda es devolver, no recordar.
func TestRemindPending_RequiresALiveMatch(t *testing.T) {
	h, d, history := newRemindingChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.competitions.On("FindByID", mock.Anything, "comp-1").Return(sevenASideCompetition(), nil)
	d.matches.On("ListByCompetition", mock.Anything, "comp-1").Return([]*match.Match{
		{ID: "match-1", CompetitionID: "comp-1", HomeTeamID: homeTeam, AwayTeamID: awayTeam, Status: match.StatusCancelled},
	}, nil)

	code, _ := remind(t, h, "user-mgr")

	assert.Equal(t, http.StatusConflict, code)
	d.charges.AssertNotCalled(t, "EnsureForMembership", mock.Anything, mock.Anything)
	assert.Empty(t, history.drafts)
}

// Un jugador raso no le recuerda deudas a sus compañeros.
func TestRemindPending_PlayerCannotRemind(t *testing.T) {
	h, d, _ := newRemindingChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)

	code, _ := remind(t, h, "user-player")

	assert.Equal(t, http.StatusForbidden, code)
	d.competitions.AssertNotCalled(t, "FindByID", mock.Anything, mock.Anything)
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
// dos ojos que justificaba que existiera el estado 'submitted'.
//
// Cubre el endpoint legado —hoy la app no confirma nada, el comprobante cierra
// el cobro— y sigue acá porque un rollback del backend lo vuelve a activar.
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

// Los fondos del equipo se listan con el nombre del partido que los dejó.
func TestFunds_ListsTeamFunds(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).Return(&membership.Membership{
		ID: "m-1", UserID: "user-player", TeamID: homeTeam,
		Role: membership.RolePlayer, Status: membership.StatusActive,
	}, nil)
	// El nombre del origen viene resuelto por el repositorio, en la misma
	// consulta que trae los fondos. El handler ya no pide una competencia por
	// entrada, así que acá tampoco hay que simularla.
	d.funds.On("ListByTeam", mock.Anything, homeTeam).Return([]*funds.Entry{
		{
			TeamID:     homeTeam,
			Source:     funds.Source{Type: funds.SourceMatchCost, ID: "comp-1"},
			SourceName: "Amistoso vs Los Rayos",
			Amount:     6000, Currency: "CLP",
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

// Los cargos del mes: la otra mitad del ingreso, junto a las cuotas.
func TestListChargesByPeriod_AnyMemberCanRead(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(&membership.Membership{
			ID: "m-1", UserID: "user-player", TeamID: homeTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)
	d.charges.On("ListByTeamAndPeriod", mock.Anything, homeTeam, 2026, 8).
		Return([]*charge.Charge{
			{ID: "ch-1", TeamID: homeTeam, Amount: 2000, Status: charge.StatusPaid},
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/charges?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusOK, w.Code)
	d.charges.AssertExpectations(t)
}

// Sin cargos devuelve lista vacía, no null.
func TestListChargesByPeriod_EmptyIsArray(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.charges.On("ListByTeamAndPeriod", mock.Anything, homeTeam, 2026, 8).Return(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/charges?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

// Un extraño no ve la recaudación del equipo.
func TestListChargesByPeriod_NonMemberIsRejected(t *testing.T) {
	h, d := newChargeHandler()

	d.members.On("FindByUserAndTeam", mock.Anything, "user-ajeno", homeTeam).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-ajeno")
	c.Params = gin.Params{{Key: "id", Value: homeTeam}}
	c.Request = httptest.NewRequest(http.MethodGet,
		"/teams/"+homeTeam+"/charges?year=2026&month=8", nil)

	h.ListByTeamAndPeriod(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.charges.AssertNotCalled(t, "ListByTeamAndPeriod",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ─── Cobros de una competencia ───────────────────────────────────────────────

func listCompetitionChargesRequest(c *gin.Context) {
	c.Params = gin.Params{{Key: "competitionId", Value: "comp-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/competitions/comp-1/charges", nil)
}

// En un amistoso los dos planteles comparten la fuente del cargo. Que el rival
// haya confirmado primero no puede dejar al plantel propio sin ver lo suyo, y
// lo del rival no se le muestra.
func TestListChargesByCompetition_OnlyOwnTeamEvenIfRivalFirst(t *testing.T) {
	h, d := newChargeHandler()

	d.charges.On("ListBySource", mock.Anything,
		charge.Source{Type: charge.SourceMatchCost, ID: "comp-1"}).
		Return([]*charge.Charge{
			{ID: "ch-away", TeamID: awayTeam, MembershipID: "m-rival", Amount: 2000},
			{ID: "ch-home", TeamID: homeTeam, MembershipID: "m-user-player", Amount: 2000},
		}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", awayTeam).
		Return(nil, membership.ErrNotFound)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-player", homeTeam).
		Return(playerOf(homeTeam, "user-player"), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-player")
	listCompetitionChargesRequest(c)

	h.ListByCompetition(c)

	require.Equal(t, http.StatusOK, w.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "ch-home", got[0]["id"])
}

// Quien no es de ninguno de los dos equipos no ve nada.
func TestListChargesByCompetition_StrangerIsRejected(t *testing.T) {
	h, d := newChargeHandler()

	d.charges.On("ListBySource", mock.Anything, mock.Anything).
		Return([]*charge.Charge{
			{ID: "ch-away", TeamID: awayTeam, MembershipID: "m-rival", Amount: 2000},
			{ID: "ch-home", TeamID: homeTeam, MembershipID: "m-home", Amount: 2000},
		}, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-ajeno", mock.Anything).
		Return(nil, membership.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-ajeno")
	listCompetitionChargesRequest(c)

	h.ListByCompetition(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ─── Condonar ────────────────────────────────────────────────────────────────

func TestWaiveCharge_ManagerClosesAPendingCharge(t *testing.T) {
	h, d := newChargeHandler()

	ch := &charge.Charge{ID: "ch-1", TeamID: homeTeam, MembershipID: "m-guest", Status: charge.StatusPending}
	d.charges.On("FindByID", mock.Anything, "ch-1").Return(ch, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.charges.On("Waive", mock.Anything, "ch-1", "user-mgr").
		Return(&charge.Charge{ID: "ch-1", TeamID: homeTeam, Status: charge.StatusWaived}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	c.Params = gin.Params{{Key: "chargeId", Value: "ch-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/ch-1/waive", nil)

	h.Waive(c)

	assert.Equal(t, http.StatusOK, w.Code)
	d.charges.AssertExpectations(t)
}

// Nadie se perdona su propia cuota: condonar es de quien maneja la plata.
func TestWaiveCharge_RejectsTheDebtor(t *testing.T) {
	h, d := newChargeHandler()

	ch := &charge.Charge{ID: "ch-1", TeamID: homeTeam, MembershipID: "m-1", Status: charge.StatusPending}
	d.charges.On("FindByID", mock.Anything, "ch-1").Return(ch, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-1", homeTeam).
		Return(&membership.Membership{
			ID: "m-1", UserID: "user-1", TeamID: homeTeam,
			Role: membership.RolePlayer, Status: membership.StatusActive,
		}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-1")
	c.Params = gin.Params{{Key: "chargeId", Value: "ch-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/ch-1/waive", nil)

	h.Waive(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	d.charges.AssertNotCalled(t, "Waive", mock.Anything, mock.Anything, mock.Anything)
}

// Perdonar una deuda ya cobrada no la borra: la convierte en plata sin registro.
func TestWaiveCharge_ConflictWhenAlreadyPaid(t *testing.T) {
	h, d := newChargeHandler()

	ch := &charge.Charge{ID: "ch-1", TeamID: homeTeam, MembershipID: "m-1", Status: charge.StatusPaid}
	d.charges.On("FindByID", mock.Anything, "ch-1").Return(ch, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "user-mgr", homeTeam).
		Return(managerOf(homeTeam, "user-mgr"), nil)
	d.charges.On("Waive", mock.Anything, "ch-1", "user-mgr").
		Return(nil, charge.ErrAlreadySettled)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "user-mgr")
	c.Params = gin.Params{{Key: "chargeId", Value: "ch-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/ch-1/waive", nil)

	h.Waive(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// El invitado tiene que poder llegar a su propio cargo: paga la cancha por la
// app como cualquiera. Lo que no puede es tocar el de otro.
func TestSubmitReceipt_GuestCanPayTheirOwnCharge(t *testing.T) {
	h, d := newChargeHandler()

	ch := &charge.Charge{ID: "ch-1", TeamID: homeTeam, MembershipID: "m-guest", Status: charge.StatusPending}
	d.charges.On("FindByID", mock.Anything, "ch-1").Return(ch, nil)
	d.members.On("FindByUserAndTeam", mock.Anything, "parche", homeTeam).
		Return(&membership.Membership{
			ID: "m-guest", UserID: "parche", TeamID: homeTeam,
			Role: membership.RolePlayer, Kind: membership.KindGuest,
			Status: membership.StatusActive,
		}, nil)
	d.charges.On("SubmitReceipt", mock.Anything, "ch-1", "file://comprobante.jpg").
		Return(&charge.Charge{ID: "ch-1", TeamID: homeTeam, Status: charge.StatusPaid}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	withClaims(c, "parche")
	c.Params = gin.Params{{Key: "chargeId", Value: "ch-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/charges/ch-1/receipt",
		strings.NewReader(`{"receipt_url":"file://comprobante.jpg"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.SubmitReceipt(c)

	assert.Equal(t, http.StatusOK, w.Code)
}
