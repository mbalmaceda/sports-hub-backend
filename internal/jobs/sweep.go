package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
)

/*
sweepExpired cierra lo que se pasó de plazo.

Un desafío de amistoso sin responder queda `expired` y su competencia,
cancelada —exactamente lo que hace rechazar, pero decidido por el reloj—. Una
invitación a torneo sin responder queda `expired` y ahí termina: el torneo sigue
para los demás, el que se quedó afuera es el equipo que no contestó.

Esto vivía dentro de los GET, corriendo una vez por cada listado. Funcionaba,
pero con dos costos: una actualización de tabla completa por request, y —el que
importaba de verdad— nada expiraba si nadie abría la app. Cualquier cosa que
leyera Postgres sin pasar por esos endpoints (un reporte, una consulta a mano)
veía estado viejo para siempre.

Sacarlo del camino de lectura es seguro por dos razones que ya estaban en su
lugar. La app **deriva** el vencimiento sin escribir nada
—`isChallengeExpired`, `isInvitationExpired`—, así que la pantalla reparte bien
aunque la barrida todavía no haya corrido; y las escrituras verifican el plazo
por su cuenta antes de aceptar cualquier cosa, así que un desafío vencido que
siga figurando `pending` no se puede aceptar igual.
*/
func sweepExpired(ctx context.Context, deps Deps, now time.Time) error {
	competitionIDs, err := deps.Friendlies.ExpireStale(ctx, now)
	if err != nil {
		return fmt.Errorf("expire stale friendlies: %w", err)
	}

	// Cancelar la competencia va aparte porque son dos tablas y el repositorio
	// de amistosos no toca competencias. Si una falla se sigue con las demás: un
	// desafío que quedó `expired` con su competencia todavía `draft` se arregla
	// en el próximo tick, y mientras tanto la app lo reparte bien igual.
	var failed error
	for _, id := range competitionIDs {
		if err := deps.Competitions.UpdateStatus(ctx, id, competition.StatusCancelled); err != nil {
			failed = fmt.Errorf("cancel competition %s: %w", id, err)
		}
	}

	if err := deps.Competitions.ExpireStaleInvitations(ctx, now); err != nil {
		return fmt.Errorf("expire stale invitations: %w", err)
	}
	return failed
}
