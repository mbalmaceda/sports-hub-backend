package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
)

// PushTokenRepository son los teléfonos donde avisarle a alguien.
//
// Vive aparte del repositorio de usuarios —donde estaba la columna
// `push_token`— porque dejó de ser un dato de la cuenta: una persona tiene
// varios dispositivos, y un dispositivo cambia de dueño cuando alguien más
// entra en él.
type PushTokenRepository struct {
	pool *pgxpool.Pool
}

func NewPushTokenRepository(pool *pgxpool.Pool) *PushTokenRepository {
	return &PushTokenRepository{pool: pool}
}

/*
Register deja el dispositivo asociado a esta cuenta.

La app lo llama en cada arranque con sesión, así que esto corre seguido y casi
siempre sin nada que cambiar: de ahí el `last_seen_at`, que es lo único que se
mueve en el caso normal.

El `ON CONFLICT` sobre el token reasigna el dispositivo al usuario nuevo. Es el
teléfono prestado o el que se vendió: Expo devuelve el mismo token para el mismo
aparato, y las notificaciones tienen que empezar a ir a quien lo está usando
ahora. Sin esto, el dueño anterior seguiría recibiendo los avisos de un club al
que ya no entra desde un teléfono que ya no tiene.
*/
func (r *PushTokenRepository) Register(
	ctx context.Context, userID, token string, at time.Time,
) error {
	if token == "" {
		return nil
	}
	const q = `
		INSERT INTO push_tokens (token, user_id, last_seen_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE
		SET user_id = EXCLUDED.user_id, last_seen_at = EXCLUDED.last_seen_at`
	if _, err := r.pool.Exec(ctx, q, token, userID, at); err != nil {
		return fmt.Errorf("pushToken.Register: %w", err)
	}
	return nil
}

// DevicesByUserIDs devuelve los teléfonos de ese grupo, con su dueño.
//
// Viene el dueño y no solo el token porque cada push carga el id de la fila de
// SU destinatario. Que la lista vuelva vacía es corriente —nadie del grupo
// instaló la app, o nadie dio permiso— y no es un error.
func (r *PushTokenRepository) DevicesByUserIDs(
	ctx context.Context, userIDs []string,
) ([]notification.Device, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	const q = `SELECT user_id, token FROM push_tokens WHERE user_id = ANY($1)`
	rows, err := r.pool.Query(ctx, q, userIDs)
	if err != nil {
		return nil, fmt.Errorf("pushToken.DevicesByUserIDs: %w", err)
	}
	defer rows.Close()

	devices := []notification.Device{}
	for rows.Next() {
		var d notification.Device
		if err := rows.Scan(&d.UserID, &d.Token); err != nil {
			return nil, fmt.Errorf("pushToken.DevicesByUserIDs scan: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pushToken.DevicesByUserIDs: %w", err)
	}
	return devices, nil
}
