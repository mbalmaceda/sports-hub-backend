/*
Package notify emite las novedades: escribe el registro y manda el push.

Es una sola puerta a propósito. La fila de `notifications` y el push son el mismo
hecho contado dos veces —una que queda y una que interrumpe—, y emitirlos por
vías separadas garantiza que algún día la lista diga una cosa y la bandeja del
teléfono otra.

El tipo que se guarda vive en `internal/domain/notification`. Acá está el verbo.
*/
package notify

import "context"

type Message struct {
	To    string
	Title string
	Body  string
	Data  map[string]string
}

// Notifier is the single interface for sending push notifications.
// Swap the implementation (Expo, FCM, APNs) without touching callers.
type Notifier interface {
	Send(ctx context.Context, msg Message) error
	SendBatch(ctx context.Context, msgs []Message) error
}
