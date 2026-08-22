package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
)

type NotificationRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// Ojo con el espacio inicial: se concatena a `SELECT`/`RETURNING` crudos, y sin
// el espacio saldría `SELECTid`.
const notificationColumns = ` id, recipient_user_id, team_id, type,
	COALESCE(entity_id::text, ''), title, body, read_at, created_at, updated_at`

/*
Las dos formas de insertar, según qué hacer si la clave ya existe.

`insertPlain` es para lo que no deduplica —quince de los dieciocho tipos— y
entra derecho: sin `dedupe_key` no hay índice que violar.

Las otras dos apuntan al índice parcial `notifications_dedupe_key`. La diferencia
entre ellas es toda la diferencia entre un recuento que se actualiza y un aviso
que ya se dio: `insertRewrite` pisa el texto, sube la fila y la vuelve a marcar
sin leer; `insertSkip` no toca nada y **no devuelve fila**, que es lo que hace
que tampoco salga el push.
*/
const notificationInsert = `
	INSERT INTO notifications
		(recipient_user_id, team_id, type, entity_id, title, body, dedupe_key)
	VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, NULLIF($7, ''))`

const insertPlain = notificationInsert + ` RETURNING` + notificationColumns

const insertRewrite = notificationInsert + `
	ON CONFLICT (recipient_user_id, dedupe_key) WHERE dedupe_key IS NOT NULL
	DO UPDATE SET
		title      = EXCLUDED.title,
		body       = EXCLUDED.body,
		updated_at = NOW(),
		read_at    = NULL
	RETURNING` + notificationColumns

const insertSkip = notificationInsert + `
	ON CONFLICT (recipient_user_id, dedupe_key) WHERE dedupe_key IS NOT NULL
	DO NOTHING
	RETURNING` + notificationColumns

func scanNotification(row pgx.Row) (*notification.Notification, error) {
	n := &notification.Notification{}
	err := row.Scan(
		&n.ID, &n.RecipientUserID, &n.TeamID, &n.Type,
		&n.EntityID, &n.Title, &n.Body, &n.ReadAt, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return n, nil
}

/*
CreateMany guarda las filas de un evento, una por destinatario, en un batch.

Cada borrador elige su sentencia según qué hacer ante una clave repetida. Van en
un batch y no en un `INSERT ... VALUES` múltiple justamente por eso: un evento
puede mezclar comportamientos, y sobre todo `DO NOTHING` y `DO UPDATE` no
conviven en la misma sentencia.

**El resultado puede tener menos filas que la entrada.** Un borrador salteado no
devuelve nada, y eso es exactamente lo que se quiere: quien ya recibió el
recordatorio de mañana no vuelve a aparecer, así que tampoco le sale el push. Es
la pieza que hace que un trabajo periódico pueda correr cada quince minutos sin
mandar noventa y seis avisos por día.
*/
func (r *NotificationRepository) CreateMany(
	ctx context.Context, drafts []notification.Draft,
) ([]*notification.Notification, error) {
	if len(drafts) == 0 {
		return nil, nil
	}

	batch := &pgx.Batch{}
	for _, d := range drafts {
		batch.Queue(insertFor(d),
			d.RecipientUserID, d.TeamID, string(d.Type), d.EntityID, d.Title, d.Body, d.DedupeKey,
		)
	}

	results := r.pool.SendBatch(ctx, batch)
	defer results.Close()

	saved := make([]*notification.Notification, 0, len(drafts))
	for range drafts {
		n, err := scanNotification(results.QueryRow())
		if errors.Is(err, pgx.ErrNoRows) {
			// Salteada: ya existía. No es un error y no corta el batch — el
			// resto de los destinatarios de este mismo evento puede no tenerla.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("notification.CreateMany: %w", err)
		}
		saved = append(saved, n)
	}
	return saved, nil
}

// insertFor elige la sentencia. Sin clave no hay conflicto posible, así que la
// política se ignora: pedir Skip sin clave sería pedir que no se guarde nada, y
// lo que corresponde ahí es guardar.
func insertFor(d notification.Draft) string {
	if d.DedupeKey == "" {
		return insertPlain
	}
	if d.OnConflict == notification.ConflictSkip {
		return insertSkip
	}
	return insertRewrite
}

/*
ListByUser devuelve el historial de una persona y cuántas no leyó.

Ordena por `updated_at` y no por `created_at` porque la fila que colapsa se
reescribe: la respuesta número doce a una citación tiene que volver arriba, no
quedarse donde nació la primera.

El no leídas viene en la misma consulta —una ventana sobre el mismo escaneo— en
vez de un segundo `SELECT COUNT`. Ojo con el detalle que eso obliga: el conteo
es sobre TODAS las no leídas del usuario, no solo las que entraron en el
`limit`, y por eso va como ventana sin filtro y no como suma de las filas
devueltas. Si contara lo que trajo, la campana mentiría apenas alguien acumule
más de `limit` novedades.
*/
func (r *NotificationRepository) ListByUser(
	ctx context.Context, userID string, limit int,
) (*notification.Feed, error) {
	const q = `
		SELECT` + notificationColumns + `,
			COUNT(*) FILTER (WHERE read_at IS NULL) OVER () AS unread
		FROM notifications
		WHERE recipient_user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("notification.ListByUser: %w", err)
	}
	defer rows.Close()

	feed := &notification.Feed{Notifications: []*notification.Notification{}}
	for rows.Next() {
		n := &notification.Notification{}
		var unread int
		if err := rows.Scan(
			&n.ID, &n.RecipientUserID, &n.TeamID, &n.Type,
			&n.EntityID, &n.Title, &n.Body, &n.ReadAt, &n.CreatedAt, &n.UpdatedAt,
			&unread,
		); err != nil {
			return nil, fmt.Errorf("notification.ListByUser scan: %w", err)
		}
		feed.Notifications = append(feed.Notifications, n)
		feed.Unread = unread
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notification.ListByUser: %w", err)
	}
	return feed, nil
}

// MarkRead marca una notificación como leída.
//
// El `recipient_user_id` del WHERE no es defensa en profundidad: es la única
// autorización que tiene este endpoint. El id viaja en la URL y sin ese filtro
// cualquiera con una sesión válida marcaría las notificaciones de otro.
//
// Volver a marcar una ya leída no hace nada y no es error: la app marca al tocar
// y el dedo repetido es lo normal.
func (r *NotificationRepository) MarkRead(
	ctx context.Context, id, userID string, at time.Time,
) error {
	const q = `
		UPDATE notifications SET read_at = $3
		WHERE id = $1 AND recipient_user_id = $2 AND read_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, id, userID, at); err != nil {
		return fmt.Errorf("notification.MarkRead: %w", err)
	}
	return nil
}

// MarkAllRead es el "Marcar todas como leídas" de la cabecera. Devuelve cuántas
// tocó para que la app sepa si valía la pena refrescar.
func (r *NotificationRepository) MarkAllRead(
	ctx context.Context, userID string, at time.Time,
) (int64, error) {
	const q = `
		UPDATE notifications SET read_at = $2
		WHERE recipient_user_id = $1 AND read_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, userID, at)
	if err != nil {
		return 0, fmt.Errorf("notification.MarkAllRead: %w", err)
	}
	return tag.RowsAffected(), nil
}
