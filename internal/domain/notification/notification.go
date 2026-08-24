/*
Package notification es el registro de novedades de una persona.

Una notificación es un hecho anotado: qué pasó, cuándo, y a qué pantalla lleva.
No es una acción. La diferencia importa porque la fila es histórica y lo que
apunta está vivo —"te retaron el 12 de agosto" sigue siendo cierto cuando el
desafío ya venció— y un botón "Aceptar" acá tendría que replicar entero el
criterio de si todavía se puede, que la pantalla de destino ya tiene.

El envío por push vive en `internal/notify`, que escribe estas filas y despacha
a Expo en el mismo paso. Nadie debería crear una fila sin mandar el push, ni al
revés.
*/
package notification

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrNotFound = errors.New("notification not found")

/*
Type es qué pasó, y es lo único que la app necesita para saber a dónde ir.

El móvil traduce tipo → ruta en un solo archivo (`notificationRoute`), así que
agregar uno acá obliga a decidir su destino allá. Los valores viajan tal cual en
el `data` del push y quedan guardados en Postgres: renombrar uno deja huérfanas
las filas viejas, así que se agregan, no se editan.

El id que acompaña (`Notification.EntityID`) lo determina el tipo. Está anotado
en cada constante porque es lo que el otro lado tiene que saber para enrutar.
*/
type Type string

const (
	// ── Competencia ──

	// TypeFriendlyChallenged: id del desafío. Al manager del equipo retado.
	TypeFriendlyChallenged Type = "friendly_challenged"
	// TypeFriendlyCountered: id del desafío. Al manager del lado que no contraofertó.
	TypeFriendlyCountered Type = "friendly_countered"
	// TypeFriendlyAccepted: id del PARTIDO, que recién ahora existe y es lo
	// accionable —convocar—, no el desafío que quedó cerrado.
	TypeFriendlyAccepted Type = "friendly_accepted"
	// TypeFriendlyDeclined: id del desafío. La competencia queda cancelada, y
	// esta fila es el único rastro de por qué desapareció de Activas.
	TypeFriendlyDeclined Type = "friendly_declined"
	// TypeTournamentInvited: id de la competencia. Al manager del invitado.
	TypeTournamentInvited Type = "tournament_invited"
	// TypeTournamentAnswered: id de la competencia. Al manager del organizador.
	TypeTournamentAnswered Type = "tournament_answered"

	// ── Partido ──

	// TypeMatchCallup: id del partido. A cada convocado.
	TypeMatchCallup Type = "match_callup"
	// TypeCallupResponses: id del partido. Al manager, y se COLAPSA: una sola
	// fila por partido que se reescribe con el recuento. Ver CollapsesByEntity.
	TypeCallupResponses Type = "callup_responses"
	// TypeMatchResult: id del partido. A los managers de los dos lados, menos
	// quien lo cargó.
	TypeMatchResult Type = "match_result"
	// TypeMatchReminder: id del partido. A los citados que todavía no
	// contestaron, la víspera. Lo dispara el reloj y no una acción, así que va
	// con clave de deduplicación: uno por persona y por partido, para siempre.
	TypeMatchReminder Type = "match_reminder"
	/*
		TypeMatchCancelled: id del partido, como el resto de los avisos de
		partido. Lleva a su resumen, que es donde queda dicho que se canceló y,
		para quien maneja la plata, a quién hay que devolverle.

		Va a los citados de los dos equipos y, si había plata de por medio, al
		manager del rival — que además de quedarse sin partido puede tener una
		transferencia que reclamar.
	*/
	TypeMatchCancelled Type = "match_cancelled"

	// ── Plata ──

	// TypeChargeCreated: id del cobro, distinto para cada destinatario. Es el
	// caso que obliga a que el payload sea por persona y no uno para todos.
	TypeChargeCreated Type = "charge_created"
	// TypePaymentReceived: id del PARTIDO. No del cobro: no existe
	// `GET /charges/:id`, y la vista del manager sobre los cobros es la sección
	// del partido.
	TypePaymentReceived Type = "payment_received"
	/*
		TypeFeeOverdue: id de la MEMBRESÍA del que debe, no de la obligación.

		Es el caso que obligó a separar `entity_id` de `dedupe_key`. El manager
		quiere ir a la ficha del jugador —ahí está su historial de cuotas— pero
		el aviso tiene que mandarse una vez por mes, no una vez en la vida de esa
		persona. Entidad: la membresía. Clave: la obligación del período.
	*/
	TypeFeeOverdue Type = "fee_overdue"
	// TypeMonthlySummary: SIN entidad. Lleva a Finanzas, que es una pestaña y no
	// una cosa. Su clave es equipo + mes.
	TypeMonthlySummary Type = "monthly_summary"

	// ── Equipo ──

	// TypeTeamInvitation: id de la invitación. Le llega a alguien que todavía
	// no tiene equipo, así que su pantalla tiene que cargar sin uno.
	TypeTeamInvitation Type = "team_invitation"
	// TypeJoinRequested: id de la solicitud. Al manager, que es quien decide.
	TypeJoinRequested Type = "join_requested"
	// TypePlayerJoined: id de la membresía nueva. Al manager.
	TypePlayerJoined Type = "player_joined"
	// TypeGuestJoined: id del partido. Al manager: un parche canjeó el enlace y
	// ya está confirmado y con su cargo.
	TypeGuestJoined Type = "guest_joined"

	/*
		TypeAnnouncement: SIN entidad. Es el aviso del manager al plantel, y el
		mensaje es todo el contenido: no hay pantalla a la que llevar.

		Es el que prueba que el modelo tiene que admitir una fila que no navega.
		Si toda notificación tuviera que enrutar, esta terminaría apuntando a
		algún lado inventado —el equipo, el inicio— solo para no quedar muerta.
	*/
	TypeAnnouncement Type = "announcement"
)

/*
Conflict es qué hacer cuando ya existe una notificación con la misma clave.

Solo aplica a las que traen `Draft.DedupeKey`; sin clave no hay conflicto posible
y cada emisión es una fila nueva, que es lo que corresponde para casi todo —dos
cobros distintos al mismo jugador son dos avisos—.

Las dos opciones existen por motivos opuestos y conviene no confundirlas:

  - Rewrite es para lo que cambia y hay que volver a mirar. Las respuestas a una
    citación: la fila se reescribe con el recuento nuevo, vuelve arriba de la
    lista y se marca sin leer otra vez, porque que el manager haya visto "8 de
    14" no significa que ya sepa que ahora son 9.
  - Skip es para lo que ya se dijo. Los avisos del reloj: el trabajo periódico
    encuentra el mismo partido de mañana en cada tick, y lo correcto es que el
    segundo intento no haga absolutamente nada.
*/
type Conflict string

const (
	// ConflictRewrite reescribe la fila existente y la vuelve a marcar sin leer.
	ConflictRewrite Conflict = "rewrite"
	// ConflictSkip deja la fila como está y no emite nada. Sin push tampoco: el
	// destinatario ya recibió este aviso.
	ConflictSkip Conflict = "skip"
)

// DedupeKey arma la clave con la que una notificación se identifica para no
// repetirse. Va por esta función y no concatenando a mano en cada llamador
// porque la clave queda guardada: dos formatos distintos para el mismo aviso son
// dos avisos, y el segundo se manda igual.
func DedupeKey(parts ...string) string {
	return strings.Join(parts, ":")
}

/*
Notification es una fila del historial.

El texto se guarda ya armado y no como plantilla más parámetros: una
notificación es lo que se dijo entonces. Si el equipo se renombra, el aviso del
mes pasado tiene que seguir diciendo el nombre que tenía cuando pasó.
*/
type Notification struct {
	ID string `json:"id"`
	// RecipientUserID es el usuario, no la membresía: el token de push está en
	// la cuenta, y la invitación a un equipo llega antes de haber membresía.
	RecipientUserID string `json:"recipient_user_id"`
	TeamID          string `json:"team_id"`
	Type            Type   `json:"type"`
	// EntityID es a qué apunta; qué cosa es lo dice Type. Vacío es una
	// notificación que solo informa y no navega.
	EntityID string `json:"entity_id,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// ReadAt es cuándo se leyó, no si se leyó: contesta más por el mismo lugar.
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// UpdatedAt es cuándo pasó lo que la fila reporta, y por lo que se ordena.
	// Igual a CreatedAt salvo en las que colapsan.
	UpdatedAt time.Time `json:"updated_at"`
}

func (n *Notification) IsRead() bool { return n.ReadAt != nil }

// Draft es una notificación por emitir. La separa de Notification que acá el
// id, las fechas y el estado de lectura todavía no existen: los pone Postgres.
type Draft struct {
	RecipientUserID string
	TeamID          string
	Type            Type
	EntityID        string
	Title           string
	Body            string
	// DedupeKey vacío significa "esto es un hecho nuevo, guardalo": es el caso
	// de las quince notificaciones que nacen de una acción. Con clave, la fila
	// es única por (destinatario, clave) y OnConflict decide qué pasa al chocar.
	DedupeKey  string
	OnConflict Conflict
}

// Feed es la respuesta del listado: las filas más lo que la campana necesita.
// El no leídas viene junto y no en otro endpoint porque la app lo pide siempre
// con la lista, y separarlo eran dos requests para pintar una pantalla.
type Feed struct {
	Notifications []*Notification `json:"notifications"`
	Unread        int             `json:"unread"`
}

type Repository interface {
	/*
		CreateMany guarda las filas de un evento, una por destinatario.

		Van juntas porque un evento produce N avisos idénticos salvo el
		destinatario —y en el caso de los cobros, salvo también el EntityID—, y
		en la misma sentencia no hay forma de que la mitad quede escrita.

		Las que traen DedupeKey se resuelven según OnConflict: reescriben la fila
		que ya existe para ese par (destinatario, clave), o no hacen nada.

		Devuelve lo guardado porque el push sale de ahí: cada mensaje viaja con
		el id de SU fila, y sin eso la app no sabría cuál marcar como leída al
		tocar la notificación. **El resultado puede ser más corto que la entrada**
		—las que se saltaron no vuelven— y eso es justamente lo que hace que un
		aviso ya emitido tampoco genere push la segunda vez.
	*/
	CreateMany(ctx context.Context, drafts []Draft) ([]*Notification, error)
	// ListByUser devuelve el historial y el no leídas de una persona, lo más
	// reciente primero, acotado a `limit`.
	ListByUser(ctx context.Context, userID string, limit int) (*Feed, error)
	// MarkRead marca una y solo si es del usuario: el id viaja en la URL y sin
	// ese filtro cualquiera marcaría las de otro.
	MarkRead(ctx context.Context, id, userID string, at time.Time) error
	// MarkAllRead es el "Marcar todas como leídas". Devuelve cuántas tocó.
	MarkAllRead(ctx context.Context, userID string, at time.Time) (int64, error)
}

/*
Device es un teléfono donde avisarle a alguien.

Viene con el dueño y no como token suelto porque cada push carga el id de LA
fila de su destinatario —para poder marcarla leída al tocarla, y para que el
cobro lleve a cada uno al suyo—. Con una lista de tokens a secas no habría cómo
saber a quién pertenece cada uno, y todos terminarían con el mismo payload.
*/
type Device struct {
	UserID string
	Token  string
}

// TokenRepository son los dispositivos donde avisarle a alguien. Es interfaz
// aparte de Repository porque son dos cosas distintas —el registro y a dónde
// mandarlo— y hay quien necesita solo una: el registro de token del arranque no
// escribe notificaciones, y los tests del servicio no quieren un historial.
type TokenRepository interface {
	// Register deja el dispositivo asociado a esta cuenta. Es idempotente, y
	// reasigna el token si venía de otro usuario: quien entra en un teléfono
	// prestado pasa a ser su dueño para el push, que es lo correcto.
	Register(ctx context.Context, userID, token string, at time.Time) error
	// DevicesByUserIDs devuelve los teléfonos de ese grupo. Una persona puede
	// tener varios; una lista vacía es corriente —nadie del grupo instaló la
	// app, o nadie dio permiso— y no es un error.
	DevicesByUserIDs(ctx context.Context, userIDs []string) ([]Device, error)
}
