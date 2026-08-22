package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
)

// sendTimeout: cuánto se espera a Expo antes de darlo por perdido. El envío
// ocurre fuera del request, así que nadie está mirando; el corte existe para no
// dejar goroutines colgadas si Expo no contesta.
const sendTimeout = 15 * time.Second

/*
Recipient es una persona a la que le llega este evento.

Trae su propio EntityID porque hay un caso donde el destino es distinto para
cada uno: al repartir el costo de la cancha, cada jugador tiene que llegar a SU
cobro. Vacío significa "el del evento", que es lo que pasa en catorce de los
quince flujos.
*/
type Recipient struct {
	UserID   string
	EntityID string
}

// To arma los destinatarios cuando todos van al mismo lado, que es el caso
// normal. Existe para que el llamador no escriba el struct completo por una
// lista de ids.
func To(userIDs ...string) []Recipient {
	rs := make([]Recipient, 0, len(userIDs))
	for _, id := range userIDs {
		if id != "" {
			rs = append(rs, Recipient{UserID: id})
		}
	}
	return rs
}

/*
Event es una cosa que pasó, dirigida a un grupo.

El texto va armado y no como plantilla: se guarda tal cual y tiene que seguir
diciendo lo mismo dentro de seis meses, cuando el equipo se haya renombrado.
*/
type Event struct {
	TeamID   string
	Type     notification.Type
	EntityID string
	Title    string
	Body     string
	// Recipients no puede incluir a quien causó el evento. Es la regla que más
	// se olvida: el manager que carga el marcador no puede recibir "cargaron el
	// marcador". Se filtra en el llamador, que es el único que sabe quién fue.
	Recipients []Recipient
	/*
		Silent deja el registro pero no manda el push.

		Existe por un solo caso y conviene no extenderlo sin pensarlo: las
		respuestas a una citación. Cada "voy" tiene que quedar en la lista para
		que el recuento esté al día, pero interrumpir al manager catorce veces
		por algo que ya ve en el contador de la nómina es la forma más rápida de
		que apague las notificaciones de la app. El "no voy" sí suena, porque ese
		sí le pide que salga a buscar un reemplazo.

		Ojo con la consecuencia: una fila silenciosa se entera recién cuando la
		persona abre la app. Solo sirve para lo que no es urgente.
	*/
	Silent bool
	/*
		DedupeKey hace que este evento sea único por destinatario.

		Vacío es lo normal: un hecho que pasó una vez se emite una vez, y de eso
		se encarga que la acción que lo produce ocurra una sola vez.

		Con clave, la fila es única por (destinatario, clave) y `OnConflict`
		decide qué pasa al repetirse. Es lo que permite que un trabajo periódico
		pregunte "¿qué partidos son mañana?" cada quince minutos: encuentra el
		mismo partido noventa y seis veces y manda un solo recordatorio. **El
		horario deja de decidir qué se manda** —eso lo decide la clave, que vive
		en Postgres— y pasa a decidir solo con cuánta latencia se revisa.

		Se arma con `notification.DedupeKey` y no concatenando a mano: la clave
		queda guardada, y dos formatos para el mismo aviso son dos avisos.
	*/
	DedupeKey  string
	OnConflict notification.Conflict
}

/*
Service emite eventos: deja el registro y avisa por push.

Emitir es best-effort y nunca es el motivo por el que alguien hizo la acción: si
Postgres o Expo fallan, el cobro se repartió igual. Por eso los errores se
registran y no vuelven al handler.

El orden importa. Primero la fila, después el push, porque el push carga el id
de la fila: sin ella no habría qué marcar como leída al tocar la notificación. Y
si la escritura falla no se manda nada — un push que lleva a una notificación
inexistente es peor que no avisar.
*/
type Service struct {
	notifier Notifier
	history  notification.Repository
	devices  notification.TokenRepository
	logger   *slog.Logger
}

func NewService(
	notifier Notifier,
	history notification.Repository,
	devices notification.TokenRepository,
	logger *slog.Logger,
) *Service {
	return &Service{notifier: notifier, history: history, devices: devices, logger: logger}
}

/*
Enabled indica si hay a dónde emitir.

Existe porque averiguar a quién avisarle cuesta consultas —los managers de un
equipo salen de leer el plantel, y los nombres de los textos salen de otra
lectura— y ese trabajo no tiene sentido si no hay servicio. Los tests de los
handlers construyen el servicio como nil justamente para eso: un test de cobros
no tiene por qué preparar el plantel para que la notificación pueda armarse.

La regla es una sola: todo lo que consulte para notificar va detrás de esto.
*/
func (s *Service) Enabled() bool { return s != nil }

/*
EmitAsync despacha en segundo plano y vuelve enseguida.

Va en una goroutine porque Expo tarda cientos de milisegundos y hacer esperar a
quien reparte un cobro por algo que no cambia el resultado no tiene sentido. El
contexto es nuevo a propósito: el del request se cancela al responder, y
cancelaría el envío justo cuando empieza.

Un Service nil no emite y no explota: así los tests de los handlers que no van
sobre notificaciones pueden pasar nil y seguir siendo legibles.
*/
func (s *Service) EmitAsync(ev Event) {
	if s == nil || len(ev.Recipients) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := s.Emit(ctx, ev); err != nil {
			s.logger.Error("could not emit the notification",
				"error", err, "type", ev.Type, "recipients", len(ev.Recipients))
		}
	}()
}

// Emit hace el trabajo y espera el resultado. Existe aparte de EmitAsync para
// poder verificarlo en los tests sin depender del scheduler.
func (s *Service) Emit(ctx context.Context, ev Event) error {
	if s == nil || len(ev.Recipients) == 0 {
		return nil
	}

	drafts := make([]notification.Draft, 0, len(ev.Recipients))
	for _, r := range ev.Recipients {
		entityID := r.EntityID
		if entityID == "" {
			entityID = ev.EntityID
		}
		drafts = append(drafts, notification.Draft{
			RecipientUserID: r.UserID,
			TeamID:          ev.TeamID,
			Type:            ev.Type,
			EntityID:        entityID,
			Title:           ev.Title,
			Body:            ev.Body,
			DedupeKey:       ev.DedupeKey,
			OnConflict:      ev.OnConflict,
		})
	}

	saved, err := s.history.CreateMany(ctx, drafts)
	if err != nil {
		return err
	}
	// Cero filas guardadas con destinatarios de entrada significa que todas se
	// saltearon: el aviso ya estaba dado. Es el caso normal de un trabajo
	// periódico y no hay nada más que hacer.
	if ev.Silent || len(saved) == 0 {
		return nil
	}

	return s.push(ctx, ev, saved)
}

// push manda un mensaje por dispositivo, cada uno con el id de la fila de SU
// dueño. Una persona con dos teléfonos recibe el mismo aviso en los dos, y los
// dos apuntan a la misma notificación: marcarla leída en uno la marca para todos
// porque el estado vive en Postgres, no en el aparato.
func (s *Service) push(ctx context.Context, ev Event, saved []*notification.Notification) error {
	byUser := make(map[string]*notification.Notification, len(saved))
	userIDs := make([]string, 0, len(saved))
	for _, n := range saved {
		if _, seen := byUser[n.RecipientUserID]; !seen {
			userIDs = append(userIDs, n.RecipientUserID)
		}
		byUser[n.RecipientUserID] = n
	}

	devices, err := s.devices.DevicesByUserIDs(ctx, userIDs)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		// Nadie del grupo tiene la app instalada con permiso concedido. Es un
		// caso corriente y no algo que valga reportar: la fila quedó escrita, y
		// van a ver la novedad la próxima vez que abran la app.
		return nil
	}

	msgs := make([]Message, 0, len(devices))
	for _, d := range devices {
		n, ok := byUser[d.UserID]
		if !ok {
			continue
		}
		msgs = append(msgs, Message{
			To:    d.Token,
			Title: n.Title,
			Body:  n.Body,
			Data:  payload(n),
		})
	}
	if len(msgs) == 0 {
		return nil
	}
	return s.notifier.SendBatch(ctx, msgs)
}

/*
payload es lo que la app recibe al tocar el push.

Son tres datos y ninguno es una ruta. El destino lo decide el móvil, en un solo
archivo (`notificationRoute`), a partir del tipo y el id: si el path lo mandara
Go, renombrar una pantalla rompería todas las notificaciones ya escritas en
Postgres, que no se pueden reescribir.

`notification_id` va porque tocar el push tiene que marcarla leída igual que
tocarla en la lista. Sin él, la campana seguiría con el punto puesto después de
haber leído el aviso.
*/
func payload(n *notification.Notification) map[string]string {
	data := map[string]string{
		"type":            string(n.Type),
		"notification_id": n.ID,
	}
	if n.EntityID != "" {
		data["entity_id"] = n.EntityID
	}
	return data
}
