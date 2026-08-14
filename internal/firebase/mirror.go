package firebase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/firestore"
)

// syncTimeout: Firestore responde en decenas de milisegundos; el corte existe
// para no dejar goroutines colgadas si la red se cae en el peor momento.
const syncTimeout = 10 * time.Second

// Membership es la proyección de una membresía en Firestore.
//
// Deliberadamente mínima: solo lo que las reglas de seguridad necesitan para
// decidir. Todo lo demás —número de camiseta, posición, fecha de ingreso— sigue
// en Postgres, que es la fuente de verdad. Este documento es una copia para que
// las reglas puedan consultarla; nada lo lee para mostrar datos.
type Membership struct {
	TeamID string
	UserID string
	Role   string
	Status string
	// Kind separa al plantel de los invitados de un partido.
	Kind string
	// MatchID es a qué partido entró el invitado, y es lo que acota lo que
	// puede leer en Firestore. Vacío para el plantel, que ve todos.
	MatchID string
}

// membershipDoc es lo que se escribe. Los nombres en minúscula son los que
// aparecen en firestore.rules: cambiarlos acá rompe las reglas en silencio.
type membershipDoc struct {
	Role   string `firestore:"role"`
	Status string `firestore:"status"`
	// kind y matchId son lo que las reglas usan para encerrar al invitado en
	// su partido. Un documento viejo no los tiene, y las reglas leen la
	// ausencia como "es del plantel": es lo que corresponde, porque antes de
	// los invitados todas las membresías lo eran.
	Kind      string    `firestore:"kind"`
	MatchID   string    `firestore:"matchId"`
	UpdatedAt time.Time `firestore:"updatedAt"`
}

func (f *Firebase) memberRef(teamID, userID string) *firestore.DocumentRef {
	return f.store.Collection("teams").Doc(teamID).Collection("members").Doc(userID)
}

// SyncMembership deja el espejo igual a lo que dice Postgres.
//
// Es idempotente a propósito: se llama después de cada cambio y también desde la
// resincronización completa, y en ambos casos el resultado tiene que ser el
// mismo documento.
func (f *Firebase) SyncMembership(ctx context.Context, m Membership) error {
	if f == nil {
		return ErrNotConfigured
	}
	_, err := f.memberRef(m.TeamID, m.UserID).Set(ctx, membershipDoc{
		Role:      m.Role,
		Status:    m.Status,
		Kind:      m.Kind,
		MatchID:   m.MatchID,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("firebase: sync membership: %w", err)
	}
	return nil
}

// RemoveMembership borra el documento. Se usa cuando la membresía deja de
// existir del todo; para una baja normal alcanza con sincronizar el estado, que
// conserva el rastro de que esa persona estuvo.
func (f *Firebase) RemoveMembership(ctx context.Context, teamID, userID string) error {
	if f == nil {
		return ErrNotConfigured
	}
	if _, err := f.memberRef(teamID, userID).Delete(ctx); err != nil {
		return fmt.Errorf("firebase: remove membership: %w", err)
	}
	return nil
}

// SyncMembershipAsync sincroniza en segundo plano y vuelve enseguida.
//
// El alta o el cambio de rol ya se guardaron en Postgres cuando esto corre: si
// Firestore no contesta, lo que se pierde es el reflejo, no el dato. Por eso el
// error se registra y no se propaga.
//
// El desfase que esto abre es real pero acotado: entre que se cambia un rol y
// que el espejo lo refleja pasan milisegundos, y mientras tanto las reglas usan
// el valor anterior. Es el mismo compromiso que tendría un trigger, sin la
// complejidad de mantenerlo.
//
// El contexto es nuevo a propósito: el del request se cancela al responder y
// cortaría la escritura justo cuando empieza.
func (f *Firebase) SyncMembershipAsync(m Membership) {
	if f == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		if err := f.SyncMembership(ctx, m); err != nil {
			slog.Error("firestore membership mirror failed",
				"error", err, "team_id", m.TeamID, "user_id", m.UserID)
		}
	}()
}
