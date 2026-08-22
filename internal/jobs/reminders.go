package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

/*
remindUpcomingMatches le recuerda la citación a quien todavía no contestó.

Es el único aviso del reloj dirigido a un jugador, y el que más pedía existir: un
partido con la nómina a medias el día anterior es un partido que el manager tiene
que salir a completar, y hasta acá se enteraba mirando.

Solo a los que están en `called`. Quien ya dijo que sí o que no no necesita nada,
y en particular al que dijo que no **no** se le insiste: la app no es para
convencer a nadie.

El texto no dice "mañana" ni ningún otro día, y no es por vaguedad. El servidor
corre en UTC y el partido se juega en Chile, así que la ventana de veinticuatro
horas cae unas veces del lado de "mañana" y otras del de "hoy"; calcularlo bien
pediría la zona del equipo, que no está guardada en ninguna parte. Además la
notificación queda en el historial, y un texto con la fecha relativa adentro
envejece mal: "tu partido es mañana" leído el jueves es falso. La fecha exacta
está en la pantalla a la que lleva.
*/
func remindUpcomingMatches(ctx context.Context, deps Deps, now time.Time) error {
	if !deps.Notifications.Enabled() {
		return nil
	}

	pending, err := deps.Matches.PendingCallupsBefore(
		ctx, now.Add(reminderWindow), now.Add(-reminderGrace),
	)
	if err != nil {
		return fmt.Errorf("list pending callups: %w", err)
	}

	/*
		Se agrupa por partido para mandar un evento por partido en vez de uno por
		persona.

		No es solo eficiencia: los del mismo partido comparten título, cuerpo y
		clave, así que son literalmente el mismo evento con varios destinatarios,
		que es la forma que `notify.Event` ya tiene. Uno por persona sería pedirle
		los dispositivos a Postgres una vez por citado.
	*/
	type group struct {
		teamID     string
		recipients []notify.Recipient
	}
	byMatch := make(map[string]*group)
	order := make([]string, 0, 8)

	for _, p := range pending {
		g, ok := byMatch[p.MatchID]
		if !ok {
			g = &group{teamID: p.TeamID}
			byMatch[p.MatchID] = g
			order = append(order, p.MatchID)
		}
		g.recipients = append(g.recipients, notify.Recipient{UserID: p.UserID})
	}

	for _, matchID := range order {
		g := byMatch[matchID]
		deps.Notifications.EmitAsync(notify.Event{
			TeamID:   g.teamID,
			Type:     notification.TypeMatchReminder,
			EntityID: matchID,
			Title:    "Todavía no confirmaste",
			Body:     "Tu partido es pronto y no dijiste si vas. Toca para responder.",
			// Uno por persona y por partido, para siempre. Es lo que permite que
			// esto corra cada quince minutos: del segundo tick en adelante, el
			// insert no escribe nada y por lo tanto no sale ningún push.
			DedupeKey:  notification.DedupeKey(string(notification.TypeMatchReminder), matchID),
			OnConflict: notification.ConflictSkip,
			Recipients: g.recipients,
		})
	}
	return nil
}
