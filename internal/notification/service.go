package notification

import (
	"context"
	"log/slog"
	"time"
)

// TokenLookup entrega los tokens de push de un conjunto de usuarios. Es una
// interfaz propia y no el repositorio de usuarios entero porque esto es todo lo
// que hace falta para notificar, y así el mock de los tests es de una línea.
type TokenLookup interface {
	PushTokensByUserIDs(ctx context.Context, userIDs []string) ([]string, error)
}

// sendTimeout: cuánto se espera a Expo antes de darlo por perdido. El envío
// ocurre fuera del request, así que nadie está mirando; el corte existe para no
// dejar goroutines colgadas si Expo no contesta.
const sendTimeout = 15 * time.Second

// Service manda notificaciones a un grupo de usuarios.
//
// Notificar es best-effort y nunca es el motivo por el que alguien hizo la
// acción: si Expo está caído o alguien no tiene token, el cobro se creó igual.
// Por eso los errores se registran y no se devuelven.
type Service struct {
	notifier Notifier
	tokens   TokenLookup
	logger   *slog.Logger
}

func NewService(notifier Notifier, tokens TokenLookup, logger *slog.Logger) *Service {
	return &Service{notifier: notifier, tokens: tokens, logger: logger}
}

// NotifyAsync despacha en segundo plano y vuelve enseguida.
//
// Va en una goroutine porque Expo tarda cientos de milisegundos y hacer esperar
// a quien reparte un cobro por algo que no cambia el resultado no tiene sentido.
// El contexto es nuevo a propósito: el del request se cancela al responder y
// cancelaría el envío justo cuando empieza.
// Un Service nil no notifica y no explota: así los tests de los handlers que no
// van sobre notificaciones pueden pasar nil y seguir siendo legibles.
func (s *Service) NotifyAsync(userIDs []string, title, body string, data map[string]string) {
	if s == nil || len(userIDs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := s.Notify(ctx, userIDs, title, body, data); err != nil {
			s.logger.Error("push notification failed", "error", err, "recipients", len(userIDs))
		}
	}()
}

// Notify manda la notificación y espera el resultado. Existe aparte de
// NotifyAsync para poder verificarlo en los tests sin depender del scheduler.
func (s *Service) Notify(ctx context.Context, userIDs []string, title, body string, data map[string]string) error {
	tokens, err := s.tokens.PushTokensByUserIDs(ctx, userIDs)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		// Nadie del grupo tiene la app instalada con permiso concedido. Es un
		// caso corriente, no algo que valga la pena reportar como error.
		return nil
	}

	msgs := make([]Message, len(tokens))
	for i, token := range tokens {
		msgs[i] = Message{To: token, Title: title, Body: body, Data: data}
	}
	return s.notifier.SendBatch(ctx, msgs)
}
