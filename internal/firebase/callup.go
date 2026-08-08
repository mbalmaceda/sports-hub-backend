package firebase

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Callup es la proyección de una convocatoria en Firestore.
//
// Solo el estado. El nombre, la camiseta y la posición no se copian: la app ya
// tiene el plantel desde la API y los une por `userID` en memoria. Duplicar
// datos personales acá obligaría además a limpiarlos al borrar una cuenta.
type Callup struct {
	TeamID  string
	MatchID string
	UserID  string
	Status  string
}

type callupDoc struct {
	Status    string    `firestore:"status"`
	UpdatedAt time.Time `firestore:"updatedAt"`
}

// SyncCallup escribe el estado de una convocatoria.
//
// Postgres se escribió primero y sigue siendo la autoridad: esto es la copia que
// los teléfonos del resto del equipo están mirando. Que Firestore falle atrasa
// el refresco de una pantalla, no pierde la convocatoria.
func (f *Firebase) SyncCallup(ctx context.Context, c Callup) error {
	if f == nil {
		return ErrNotConfigured
	}
	_, err := f.store.
		Collection("teams").Doc(c.TeamID).
		Collection("matches").Doc(c.MatchID).
		Collection("callups").Doc(c.UserID).
		Set(ctx, callupDoc{Status: c.Status, UpdatedAt: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("firebase: sync callup: %w", err)
	}
	return nil
}

// SyncCallupsAsync proyecta varias convocatorias en segundo plano.
//
// Convocar cita a once o más personas de una vez y el manager no tiene por qué
// esperar a que Firestore confirme cada una: en Postgres ya quedaron.
func (f *Firebase) SyncCallupsAsync(callups []Callup) {
	if f == nil || len(callups) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		for _, c := range callups {
			if err := f.SyncCallup(ctx, c); err != nil {
				slog.Error("firestore callup mirror failed",
					"error", err, "match_id", c.MatchID, "user_id", c.UserID)
			}
		}
	}()
}
