package notification_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

type fakeNotifier struct {
	sent []notification.Message
	err  error
}

func (f *fakeNotifier) Send(ctx context.Context, msg notification.Message) error {
	return f.SendBatch(ctx, []notification.Message{msg})
}

func (f *fakeNotifier) SendBatch(_ context.Context, msgs []notification.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msgs...)
	return nil
}

type fakeTokens struct {
	tokens    []string
	err       error
	askedFor  []string
	callCount int
}

func (f *fakeTokens) PushTokensByUserIDs(_ context.Context, userIDs []string) ([]string, error) {
	f.callCount++
	f.askedFor = userIDs
	return f.tokens, f.err
}

func newService(n notification.Notifier, t notification.TokenLookup) *notification.Service {
	return notification.NewService(n, t, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestNotify_SendsOneMessagePerToken(t *testing.T) {
	notifier := &fakeNotifier{}
	tokens := &fakeTokens{tokens: []string{"ExponentPushToken[aaa]", "ExponentPushToken[bbb]"}}
	svc := newService(notifier, tokens)

	err := svc.Notify(context.Background(), []string{"user-1", "user-2"},
		"Te convocaron", "Estás citado.", map[string]string{"type": "callup_created"})

	require.NoError(t, err)
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, "ExponentPushToken[aaa]", notifier.sent[0].To)
	assert.Equal(t, "ExponentPushToken[bbb]", notifier.sent[1].To)
	assert.Equal(t, "Te convocaron", notifier.sent[0].Title)
	assert.Equal(t, "callup_created", notifier.sent[0].Data["type"])
	assert.Equal(t, []string{"user-1", "user-2"}, tokens.askedFor)
}

// Que nadie del grupo tenga la app instalada es corriente, no un error, y sobre
// todo no puede terminar en una llamada a Expo con la lista vacía.
func TestNotify_NoTokensDoesNotCallTheNotifier(t *testing.T) {
	notifier := &fakeNotifier{}
	svc := newService(notifier, &fakeTokens{tokens: nil})

	err := svc.Notify(context.Background(), []string{"user-1"}, "Título", "Cuerpo", nil)

	require.NoError(t, err)
	assert.Empty(t, notifier.sent)
}

func TestNotify_PropagatesLookupError(t *testing.T) {
	notifier := &fakeNotifier{}
	svc := newService(notifier, &fakeTokens{err: errors.New("db caída")})

	err := svc.Notify(context.Background(), []string{"user-1"}, "Título", "Cuerpo", nil)

	assert.Error(t, err)
	assert.Empty(t, notifier.sent)
}

// Sin destinatarios no hay a quién preguntarle el token: ni siquiera se consulta.
func TestNotify_EmptyRecipientsIsANoop(t *testing.T) {
	tokens := &fakeTokens{}
	svc := newService(&fakeNotifier{}, tokens)

	svc.NotifyAsync(nil, "Título", "Cuerpo", nil)

	assert.Zero(t, tokens.callCount)
}

// Los handlers de los tests construyen el servicio como nil a propósito; si eso
// hiciera panic, cualquier test de cobros o convocatorias se caería.
func TestNotifyAsync_NilServiceDoesNotPanic(t *testing.T) {
	var svc *notification.Service

	assert.NotPanics(t, func() {
		svc.NotifyAsync([]string{"user-1"}, "Título", "Cuerpo", nil)
	})
}
