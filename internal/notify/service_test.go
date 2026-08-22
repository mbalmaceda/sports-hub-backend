package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

type fakeNotifier struct {
	sent []notify.Message
	err  error
}

func (f *fakeNotifier) Send(ctx context.Context, msg notify.Message) error {
	return f.SendBatch(ctx, []notify.Message{msg})
}

func (f *fakeNotifier) SendBatch(_ context.Context, msgs []notify.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msgs...)
	return nil
}

// fakeHistory devuelve filas con id predecible para poder afirmar que cada push
// se llevó el id de SU destinatario y no el de otro.
type fakeHistory struct {
	saved []notification.Draft
	err   error
}

func (f *fakeHistory) CreateMany(
	_ context.Context, drafts []notification.Draft,
) ([]*notification.Notification, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.saved = append(f.saved, drafts...)
	out := make([]*notification.Notification, len(drafts))
	for i, d := range drafts {
		out[i] = &notification.Notification{
			ID:              "notif-" + d.RecipientUserID,
			RecipientUserID: d.RecipientUserID,
			TeamID:          d.TeamID,
			Type:            d.Type,
			EntityID:        d.EntityID,
			Title:           d.Title,
			Body:            d.Body,
		}
	}
	return out, nil
}

func (f *fakeHistory) ListByUser(context.Context, string, int) (*notification.Feed, error) {
	return nil, nil
}
func (f *fakeHistory) MarkRead(context.Context, string, string, time.Time) error { return nil }
func (f *fakeHistory) MarkAllRead(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

type fakeDevices struct {
	devices   []notification.Device
	err       error
	askedFor  []string
	callCount int
}

func (f *fakeDevices) Register(context.Context, string, string, time.Time) error { return nil }

func (f *fakeDevices) DevicesByUserIDs(
	_ context.Context, userIDs []string,
) ([]notification.Device, error) {
	f.callCount++
	f.askedFor = userIDs
	return f.devices, f.err
}

func newService(n notify.Notifier, h notification.Repository, d notification.TokenRepository) *notify.Service {
	return notify.NewService(n, h, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func event(recipients ...notify.Recipient) notify.Event {
	return notify.Event{
		TeamID:     "team-1",
		Type:       notification.TypeMatchCallup,
		EntityID:   "match-1",
		Title:      "Te convocaron",
		Body:       "Estás citado para un partido.",
		Recipients: recipients,
	}
}

func TestEmit_WritesOneRowPerRecipientAndPushesToEveryDevice(t *testing.T) {
	notifier := &fakeNotifier{}
	history := &fakeHistory{}
	devices := &fakeDevices{devices: []notification.Device{
		{UserID: "user-1", Token: "ExponentPushToken[aaa]"},
		{UserID: "user-2", Token: "ExponentPushToken[bbb]"},
	}}

	err := newService(notifier, history, devices).
		Emit(context.Background(), event(notify.To("user-1", "user-2")...))

	require.NoError(t, err)
	require.Len(t, history.saved, 2)
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, []string{"user-1", "user-2"}, devices.askedFor)
	assert.Equal(t, "Te convocaron", notifier.sent[0].Title)
	assert.Equal(t, "match_callup", notifier.sent[0].Data["type"])
	assert.Equal(t, "match-1", notifier.sent[0].Data["entity_id"])
}

// El teléfono viejo y el nuevo reciben lo mismo, y los dos apuntan a la misma
// fila: el estado de leída vive en Postgres, no en el aparato.
func TestEmit_OnePushPerDeviceOfTheSamePerson(t *testing.T) {
	notifier := &fakeNotifier{}
	devices := &fakeDevices{devices: []notification.Device{
		{UserID: "user-1", Token: "ExponentPushToken[phone]"},
		{UserID: "user-1", Token: "ExponentPushToken[tablet]"},
	}}

	err := newService(notifier, &fakeHistory{}, devices).
		Emit(context.Background(), event(notify.To("user-1")...))

	require.NoError(t, err)
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, "notif-user-1", notifier.sent[0].Data["notification_id"])
	assert.Equal(t, "notif-user-1", notifier.sent[1].Data["notification_id"])
}

// El caso que obligó a que el payload sea por persona: al repartir el costo de
// la cancha, cada jugador tiene que llegar a SU cobro y no al de otro.
func TestEmit_EachRecipientKeepsItsOwnEntity(t *testing.T) {
	notifier := &fakeNotifier{}
	history := &fakeHistory{}
	devices := &fakeDevices{devices: []notification.Device{
		{UserID: "user-1", Token: "token-1"},
		{UserID: "user-2", Token: "token-2"},
	}}

	ev := notify.Event{
		TeamID: "team-1",
		Type:   notification.TypeChargeCreated,
		Title:  "Nuevo cobro",
		Body:   "Se repartió el costo de la cancha.",
		Recipients: []notify.Recipient{
			{UserID: "user-1", EntityID: "charge-1"},
			{UserID: "user-2", EntityID: "charge-2"},
		},
	}
	require.NoError(t, newService(notifier, history, devices).Emit(context.Background(), ev))

	require.Len(t, notifier.sent, 2)
	byToken := map[string]string{}
	for _, m := range notifier.sent {
		byToken[m.To] = m.Data["entity_id"]
	}
	assert.Equal(t, "charge-1", byToken["token-1"])
	assert.Equal(t, "charge-2", byToken["token-2"])
}

// Silent es para las respuestas a una citación: la lista se mantiene al día sin
// hacer sonar el teléfono del manager catorce veces.
func TestEmit_SilentRecordsWithoutPushing(t *testing.T) {
	notifier := &fakeNotifier{}
	history := &fakeHistory{}
	devices := &fakeDevices{devices: []notification.Device{{UserID: "user-1", Token: "token-1"}}}

	ev := event(notify.To("user-1")...)
	ev.Silent = true
	require.NoError(t, newService(notifier, history, devices).Emit(context.Background(), ev))

	assert.Len(t, history.saved, 1, "la fila tiene que quedar igual")
	assert.Empty(t, notifier.sent, "pero sin interrumpir")
	assert.Zero(t, devices.callCount, "ni siquiera se preguntan los dispositivos")
}

// Que nadie del grupo tenga la app instalada es corriente, no un error, y sobre
// todo no puede terminar en una llamada a Expo con la lista vacía.
func TestEmit_NoDevicesDoesNotCallTheNotifier(t *testing.T) {
	notifier := &fakeNotifier{}
	history := &fakeHistory{}

	err := newService(notifier, history, &fakeDevices{}).
		Emit(context.Background(), event(notify.To("user-1")...))

	require.NoError(t, err)
	assert.Len(t, history.saved, 1, "la fila queda escrita igual")
	assert.Empty(t, notifier.sent)
}

// Si la fila no se pudo escribir no se manda el push: llevaría a una
// notificación que no existe, y no habría qué marcar como leída al tocarla.
func TestEmit_DoesNotPushWhenTheRowCouldNotBeWritten(t *testing.T) {
	notifier := &fakeNotifier{}
	devices := &fakeDevices{devices: []notification.Device{{UserID: "user-1", Token: "token-1"}}}

	err := newService(notifier, &fakeHistory{err: errors.New("base caída")}, devices).
		Emit(context.Background(), event(notify.To("user-1")...))

	assert.Error(t, err)
	assert.Empty(t, notifier.sent)
	assert.Zero(t, devices.callCount)
}

func TestEmit_PropagatesDeviceLookupError(t *testing.T) {
	notifier := &fakeNotifier{}

	err := newService(notifier, &fakeHistory{}, &fakeDevices{err: errors.New("base caída")}).
		Emit(context.Background(), event(notify.To("user-1")...))

	assert.Error(t, err)
	assert.Empty(t, notifier.sent)
}

// Sin destinatarios no hay nada que escribir ni a quién preguntarle el token.
func TestEmit_EmptyRecipientsIsANoop(t *testing.T) {
	history := &fakeHistory{}
	devices := &fakeDevices{}

	newService(&fakeNotifier{}, history, devices).EmitAsync(event())

	assert.Empty(t, history.saved)
	assert.Zero(t, devices.callCount)
}

// To descarta los ids vacíos: los resolutores de destinatarios devuelven listas
// que pueden traerlos, y una fila con recipient vacío revienta contra la FK.
func TestTo_SkipsEmptyIDs(t *testing.T) {
	assert.Equal(t,
		[]notify.Recipient{{UserID: "user-1"}, {UserID: "user-2"}},
		notify.To("user-1", "", "user-2"))
}

// Los handlers de los tests construyen el servicio como nil a propósito; si eso
// hiciera panic, cualquier test de cobros o convocatorias se caería.
func TestEmitAsync_NilServiceDoesNotPanic(t *testing.T) {
	var svc *notify.Service

	assert.NotPanics(t, func() {
		svc.EmitAsync(event(notify.To("user-1")...))
	})
}
