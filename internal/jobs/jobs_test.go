package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

// captureNotifier se queda con lo que se emitió, para poder afirmar sobre el
// contenido de los avisos y no solo sobre que se llamó a alguien.
type captureNotifier struct{ sent []notify.Message }

func (c *captureNotifier) Send(ctx context.Context, msg notify.Message) error {
	return c.SendBatch(ctx, []notify.Message{msg})
}

func (c *captureNotifier) SendBatch(_ context.Context, msgs []notify.Message) error {
	c.sent = append(c.sent, msgs...)
	return nil
}

// captureHistory hace de tabla de notificaciones, con la deduplicación incluida:
// es justamente lo que estos trabajos apoyan, así que un doble sin ella no
// probaría nada.
type captureHistory struct {
	drafts []notification.Draft
	keys   map[string]bool
}

func newHistory() *captureHistory { return &captureHistory{keys: map[string]bool{}} }

func (h *captureHistory) CreateMany(
	_ context.Context, drafts []notification.Draft,
) ([]*notification.Notification, error) {
	out := make([]*notification.Notification, 0, len(drafts))
	for _, d := range drafts {
		key := d.RecipientUserID + "|" + d.DedupeKey
		if d.DedupeKey != "" && h.keys[key] {
			if d.OnConflict == notification.ConflictSkip {
				continue
			}
		}
		if d.DedupeKey != "" {
			h.keys[key] = true
		}
		h.drafts = append(h.drafts, d)
		out = append(out, &notification.Notification{
			ID: "n-" + d.RecipientUserID, RecipientUserID: d.RecipientUserID,
			Type: d.Type, EntityID: d.EntityID, Title: d.Title, Body: d.Body,
		})
	}
	return out, nil
}

func (h *captureHistory) ListByUser(context.Context, string, int) (*notification.Feed, error) {
	return nil, nil
}
func (h *captureHistory) MarkRead(context.Context, string, string, time.Time) error { return nil }
func (h *captureHistory) MarkAllRead(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

type oneDevice struct{}

func (oneDevice) Register(context.Context, string, string, time.Time) error { return nil }
func (oneDevice) DevicesByUserIDs(_ context.Context, userIDs []string) ([]notification.Device, error) {
	devices := make([]notification.Device, 0, len(userIDs))
	for _, id := range userIDs {
		devices = append(devices, notification.Device{UserID: id, Token: "token-" + id})
	}
	return devices, nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// runJob corre un trabajo en forma síncrona. `EmitAsync` despacha en una
// goroutine, así que hay que darle lugar antes de mirar el resultado.
func runJob(t *testing.T, run func() error) {
	t.Helper()
	require.NoError(t, run())
	// El envío es best-effort y fuera del request; esperar un instante es más
	// simple que instrumentar el servicio solo para el test.
	time.Sleep(150 * time.Millisecond)
}

func contains(bodies []string, month string) bool {
	for _, b := range bodies {
		if strings.Contains(b, month) {
			return true
		}
	}
	return false
}

func manager(teamID, userID, membershipID string) *membership.TeamMember {
	return &membership.TeamMember{
		MembershipID: membershipID, UserID: userID, TeamID: teamID,
		FullName: "Ana Manager", Role: membership.RoleManager,
		Kind: membership.KindMember, Status: membership.StatusActive,
	}
}

// ─── Las expresiones ────────────────────────────────────────────────────────

// Una expresión mal escrita deja su trabajo sin correr y solo se nota en
// producción, meses después, cuando alguien pregunta por qué no llegó un aviso.
func TestSchedules_AreValidCronExpressions(t *testing.T) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, spec := range []string{"*/10 * * * *", "*/15 * * * *", "0 9 * * *", "0 9 1 * *"} {
		_, err := parser.Parse(spec)
		assert.NoError(t, err, "expresión inválida: %s", spec)
	}
}

// La zona tiene que existir en la imagen. Sin tzdata, `LoadLocation` falla y los
// avisos con hora fija salen en UTC, o sea de madrugada en Chile.
func TestSchedulerZone_IsAvailable(t *testing.T) {
	_, err := time.LoadLocation("America/Santiago")
	assert.NoError(t, err)
}

// ─── Barrida ────────────────────────────────────────────────────────────────

func TestSweepExpired_CancelsTheCompetitionOfEachExpiredChallenge(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	fr.On("ExpireStale", mock.Anything, mock.Anything).Return([]string{"comp-1", "comp-2"}, nil)
	cr.On("UpdateStatus", mock.Anything, "comp-1", competition.StatusCancelled).Return(nil)
	cr.On("UpdateStatus", mock.Anything, "comp-2", competition.StatusCancelled).Return(nil)
	cr.On("ExpireStaleInvitations", mock.Anything, mock.Anything).Return(nil)

	require.NoError(t, sweepExpired(context.Background(),
		Deps{Friendlies: fr, Competitions: cr}, time.Now()))

	cr.AssertExpectations(t)
}

// Las invitaciones a torneo se vencen aunque falle el amistoso, y viceversa: son
// dos barridas independientes y una caída no puede tapar a la otra.
func TestSweepExpired_ReportsFailureButKeepsGoing(t *testing.T) {
	fr := &testutil.MockFriendlyRepo{}
	cr := &testutil.MockCompetitionRepo{}
	fr.On("ExpireStale", mock.Anything, mock.Anything).Return([]string{"comp-1"}, nil)
	cr.On("UpdateStatus", mock.Anything, "comp-1", competition.StatusCancelled).
		Return(errors.New("boom"))
	cr.On("ExpireStaleInvitations", mock.Anything, mock.Anything).Return(nil)

	err := sweepExpired(context.Background(),
		Deps{Friendlies: fr, Competitions: cr}, time.Now())

	assert.Error(t, err)
	cr.AssertCalled(t, "ExpireStaleInvitations", mock.Anything, mock.Anything)
}

// ─── Recordatorio de partido ────────────────────────────────────────────────

func newNotifier() (*notify.Service, *captureNotifier, *captureHistory) {
	pusher := &captureNotifier{}
	history := newHistory()
	return notify.NewService(pusher, history, oneDevice{}, silentLogger()), pusher, history
}

func TestRemindUpcomingMatches_OnePerMatchWithEveryoneWhoDidNotAnswer(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	svc, pusher, history := newNotifier()
	soon := time.Now().Add(20 * time.Hour)
	mr.On("PendingCallupsBefore", mock.Anything, mock.Anything, mock.Anything).
		Return([]*match.PendingCallup{
			{MatchID: "match-1", TeamID: "team-1", UserID: "user-a", ScheduledAt: soon},
			{MatchID: "match-1", TeamID: "team-1", UserID: "user-b", ScheduledAt: soon},
			{MatchID: "match-2", TeamID: "team-1", UserID: "user-c", ScheduledAt: soon},
		}, nil)

	runJob(t, func() error {
		return remindUpcomingMatches(context.Background(),
			Deps{Matches: mr, Notifications: svc}, time.Now())
	})

	require.Len(t, history.drafts, 3, "una fila por citado sin responder")
	assert.Len(t, pusher.sent, 3)
	for _, d := range history.drafts {
		assert.Equal(t, notification.TypeMatchReminder, d.Type)
		assert.Equal(t, notification.ConflictSkip, d.OnConflict)
		assert.Equal(t, "match_reminder:"+d.EntityID, d.DedupeKey)
	}
}

/*
El corazón del diseño: el trabajo corre cada quince minutos y encuentra el mismo
partido siempre, pero el aviso sale una sola vez.

Sin esto, un partido de mañana genera noventa y seis push por persona.
*/
func TestRemindUpcomingMatches_SecondRunSendsNothing(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	svc, pusher, history := newNotifier()
	pending := []*match.PendingCallup{
		{MatchID: "match-1", TeamID: "team-1", UserID: "user-a", ScheduledAt: time.Now().Add(20 * time.Hour)},
	}
	mr.On("PendingCallupsBefore", mock.Anything, mock.Anything, mock.Anything).Return(pending, nil)

	deps := Deps{Matches: mr, Notifications: svc}
	for i := 0; i < 5; i++ {
		runJob(t, func() error {
			return remindUpcomingMatches(context.Background(), deps, time.Now())
		})
	}

	assert.Len(t, history.drafts, 1, "una fila, no cinco")
	assert.Len(t, pusher.sent, 1, "y un solo push")
}

// ─── Cuotas vencidas ────────────────────────────────────────────────────────

func TestAnnounceOverdueFees_GoesToTheManagerAndPointsAtThePlayer(t *testing.T) {
	fees := &testutil.MockFeeRepo{}
	memr := &testutil.MockMembershipRepo{}
	svc, _, history := newNotifier()

	fees.On("ListOverdue", mock.Anything, mock.Anything).Return([]*fee.Overdue{{
		ObligationID: "obl-1", TeamID: "team-1", MembershipID: "ms-jugador",
		FullName: "Kai Müller", PeriodYear: 2026, PeriodMonth: 6,
		Amount: 20000, Currency: "CLP",
	}}, nil)
	memr.On("ListByTeam", mock.Anything, "team-1").
		Return([]*membership.TeamMember{manager("team-1", "user-mgr", "ms-mgr")}, nil)

	runJob(t, func() error {
		return announceOverdueFees(context.Background(),
			Deps{Fees: fees, Memberships: memr, Notifications: svc}, time.Now())
	})

	require.Len(t, history.drafts, 1)
	d := history.drafts[0]
	assert.Equal(t, "user-mgr", d.RecipientUserID, "lo recibe quien maneja la plata")
	assert.Equal(t, "ms-jugador", d.EntityID, "y lleva a la ficha del que debe")
	assert.Equal(t, "fee_overdue:obl-1", d.DedupeKey, "una vez por mes, no una en la vida")
	assert.Contains(t, d.Body, "Kai Müller")
	assert.Contains(t, d.Body, "junio")
}

// La entidad y la clave apuntan a cosas distintas a propósito: la ficha del
// jugador es la misma todos los meses, la obligación no. Si la clave fuera la
// membresía, el aviso saldría una sola vez en la vida de esa persona.
func TestAnnounceOverdueFees_NextMonthNotifiesAgain(t *testing.T) {
	fees := &testutil.MockFeeRepo{}
	memr := &testutil.MockMembershipRepo{}
	svc, _, history := newNotifier()
	memr.On("ListByTeam", mock.Anything, "team-1").
		Return([]*membership.TeamMember{manager("team-1", "user-mgr", "ms-mgr")}, nil)

	junio := &fee.Overdue{ObligationID: "obl-junio", TeamID: "team-1", MembershipID: "ms-jugador",
		FullName: "Kai Müller", PeriodMonth: 6, Currency: "CLP"}
	julio := &fee.Overdue{ObligationID: "obl-julio", TeamID: "team-1", MembershipID: "ms-jugador",
		FullName: "Kai Müller", PeriodMonth: 7, Currency: "CLP"}

	fees.On("ListOverdue", mock.Anything, mock.Anything).
		Return([]*fee.Overdue{junio, julio}, nil)

	deps := Deps{Fees: fees, Memberships: memr, Notifications: svc}
	runJob(t, func() error {
		return announceOverdueFees(context.Background(), deps, time.Now())
	})
	runJob(t, func() error {
		return announceOverdueFees(context.Background(), deps, time.Now())
	})

	require.Len(t, history.drafts, 2, "una por período, y ninguna repetida")
	// Sin asumir orden: cada cuota se emite en su propia goroutine, así que dos
	// del mismo jugador llegan en el orden que quieran. Que sean independientes
	// es justamente el punto —una no espera a la otra—.
	bodies := []string{history.drafts[0].Body, history.drafts[1].Body}
	assert.Condition(t, func() bool {
		return contains(bodies, "junio") && contains(bodies, "julio")
	}, "faltó un mes: %v", bodies)
}

// ─── Resumen mensual ────────────────────────────────────────────────────────

func TestAnnounceMonthlySummary_NamesTheMonthThatClosed(t *testing.T) {
	teams := &testutil.MockTeamRepo{}
	memr := &testutil.MockMembershipRepo{}
	svc, _, history := newNotifier()

	teams.On("List", mock.Anything).Return([]*team.Team{{ID: "team-1", Name: "Halcones"}}, nil)
	memr.On("ListByTeam", mock.Anything, "team-1").
		Return([]*membership.TeamMember{manager("team-1", "user-mgr", "ms-mgr")}, nil)

	// El día 1 de agosto se resume julio.
	agosto := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	runJob(t, func() error {
		return announceMonthlySummary(context.Background(),
			Deps{Teams: teams, Memberships: memr, Notifications: svc}, agosto)
	})

	require.Len(t, history.drafts, 1)
	d := history.drafts[0]
	assert.Contains(t, d.Title, "julio", "el mes que cerró, no el que arranca")
	assert.Empty(t, d.EntityID, "no apunta a nada: lleva a Finanzas")
	assert.Equal(t, "monthly_summary:team-1:2026-7", d.DedupeKey)
}

// Correr dos veces el mismo día 1 —un redespliegue, un reintento— no manda dos
// resúmenes.
func TestAnnounceMonthlySummary_OncePerTeamAndMonth(t *testing.T) {
	teams := &testutil.MockTeamRepo{}
	memr := &testutil.MockMembershipRepo{}
	svc, pusher, history := newNotifier()

	teams.On("List", mock.Anything).Return([]*team.Team{{ID: "team-1", Name: "Halcones"}}, nil)
	memr.On("ListByTeam", mock.Anything, "team-1").
		Return([]*membership.TeamMember{manager("team-1", "user-mgr", "ms-mgr")}, nil)

	agosto := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	deps := Deps{Teams: teams, Memberships: memr, Notifications: svc}
	runJob(t, func() error { return announceMonthlySummary(context.Background(), deps, agosto) })
	runJob(t, func() error { return announceMonthlySummary(context.Background(), deps, agosto) })

	assert.Len(t, history.drafts, 1)
	assert.Len(t, pusher.sent, 1)
}

// Sin servicio de notificaciones los trabajos no consultan nada. Es lo que
// permite construirlos en pruebas de otras cosas sin preparar planteles.
func TestJobs_WithoutNotificationsDoNothing(t *testing.T) {
	mr := &testutil.MockMatchRepo{}
	fees := &testutil.MockFeeRepo{}
	teams := &testutil.MockTeamRepo{}

	deps := Deps{Matches: mr, Fees: fees, Teams: teams}
	assert.NoError(t, remindUpcomingMatches(context.Background(), deps, time.Now()))
	assert.NoError(t, announceOverdueFees(context.Background(), deps, time.Now()))
	assert.NoError(t, announceMonthlySummary(context.Background(), deps, time.Now()))

	mr.AssertNotCalled(t, "PendingCallupsBefore", mock.Anything, mock.Anything, mock.Anything)
	fees.AssertNotCalled(t, "ListOverdue", mock.Anything, mock.Anything)
	teams.AssertNotCalled(t, "List", mock.Anything)
}
