package jobs

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

// monthNames son los meses en español para el texto de los avisos.
//
// A mano y no con `time.Month.String()`, que devuelve inglés, ni con una
// librería de localización: son doce palabras y es el único lugar del backend
// que las necesita. Los textos de las notificaciones se guardan armados, así que
// esto tiene que salir bien acá y no hay una segunda oportunidad del lado de la
// app.
var monthNames = [...]string{
	"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
}

func monthName(month int) string {
	if month < 1 || month > 12 {
		return ""
	}
	return monthNames[month-1]
}

/*
announceOverdueFees le avisa a quien maneja la plata que una cuota no se pagó.

Va al manager y al tesorero, no al que debe, y eso no es un descuido: **hoy el
jugador no tiene dónde ver su cuota mensual**. La pestaña de Finanzas está detrás
de `useCan('finances.view')` y no hay pantalla propia de cuota; mandarle una
notificación a un lugar que no puede abrir sería peor que no mandarla. Cuando la
Fase 3 le dé su vista de finanzas, el aviso al deudor entra por acá mismo.

Una fila por persona y por período, que es lo que muestra el diseño ("Kai Müller
no pagó junio"). Con la nómina entera atrasada son catorce filas, y está bien:
son catorce hechos distintos y cada uno lleva a una ficha distinta. Lo que no
puede pasar es que se repitan mes a mes, y de eso se encarga la clave.
*/
func announceOverdueFees(ctx context.Context, deps Deps, now time.Time) error {
	if !deps.Notifications.Enabled() {
		return nil
	}

	overdue, err := deps.Fees.ListOverdue(ctx, now)
	if err != nil {
		return fmt.Errorf("list overdue fees: %w", err)
	}
	if len(overdue) == 0 {
		return nil
	}

	// El plantel se lee una vez por equipo y no una por cuota: un equipo con
	// diez atrasos son diez avisos a los mismos dos managers.
	recipientsByTeam := make(map[string][]notify.Recipient)

	for _, o := range overdue {
		recipients, ok := recipientsByTeam[o.TeamID]
		if !ok {
			roster, err := deps.Memberships.ListByTeam(ctx, o.TeamID)
			if err != nil {
				return fmt.Errorf("read roster of team %s: %w", o.TeamID, err)
			}
			recipients = notify.To(membership.MoneyHandlerUserIDs(roster)...)
			recipientsByTeam[o.TeamID] = recipients
		}
		if len(recipients) == 0 {
			continue
		}

		deps.Notifications.EmitAsync(notify.Event{
			TeamID: o.TeamID,
			Type:   notification.TypeFeeOverdue,
			// La membresía, no la obligación: el manager quiere ir a la ficha
			// del jugador, donde está su historial de cuotas.
			EntityID: o.MembershipID,
			Title:    "Cuota sin pagar",
			Body:     o.FullName + " no pagó la cuota de " + monthName(o.PeriodMonth) + ".",
			// La obligación, no la membresía: uno por mes, no uno en la vida.
			// Es el caso que obligó a que la clave y la entidad sean columnas
			// distintas.
			DedupeKey:  notification.DedupeKey(string(notification.TypeFeeOverdue), o.ObligationID),
			OnConflict: notification.ConflictSkip,
			Recipients: recipients,
		})
	}
	return nil
}

/*
announceMonthlySummary abre el mes avisando que el anterior ya se puede mirar.

Es el aviso más flojo de los dieciocho y aun así vale: el resumen contesta "¿cómo
venimos?" y nadie entra a Finanzas por las dudas el día 1. Cerrar el mes es
justamente cuando el número sirve.

La expresión del cron lo pone el día 1 a las 9. Antes de eso el trabajo llevaba
adentro un `if now.Day() != 1`, que es la clase de cosa que un scheduler de
verdad saca del cuerpo del trabajo: el cuándo es del horario y el qué, del
trabajo.

El texto no trae cifras a propósito. El resumen se **deriva** —cruzar las
competencias del mes con los cobros y los gastos— y ese cálculo vive entero del
lado de la app, en `monthlyMatchEconomics`. Ponerle un número acá obligaría a
portarlo a Go y a mantener las dos versiones de acuerdo, y la primera vez que
discrepen el aviso va a decir una cosa y la pantalla otra. Prefiere no decir un
número antes que decir uno que no coincide.
*/
func announceMonthlySummary(ctx context.Context, deps Deps, now time.Time) error {
	if !deps.Notifications.Enabled() {
		return nil
	}

	// El mes que cerró, no el que arranca.
	previous := now.AddDate(0, -1, 0)
	period := strconv.Itoa(previous.Year()) + "-" + strconv.Itoa(int(previous.Month()))

	teams, err := deps.Teams.List(ctx)
	if err != nil {
		return fmt.Errorf("list teams: %w", err)
	}

	for _, t := range teams {
		roster, err := deps.Memberships.ListByTeam(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("read roster of team %s: %w", t.ID, err)
		}
		recipients := notify.To(membership.MoneyHandlerUserIDs(roster)...)
		if len(recipients) == 0 {
			continue
		}

		deps.Notifications.EmitAsync(notify.Event{
			TeamID: t.ID,
			Type:   notification.TypeMonthlySummary,
			// Sin entidad: lleva a Finanzas, que es una pestaña y no una cosa.
			Title: "Resumen de " + monthName(int(previous.Month())),
			Body:  "Ya puedes ver cómo cerró el mes en Finanzas.",
			// Equipo y mes. Es la clave que no se puede escribir en `entity_id`,
			// que es UUID, y por eso `dedupe_key` es TEXT.
			DedupeKey: notification.DedupeKey(
				string(notification.TypeMonthlySummary), t.ID, period,
			),
			OnConflict: notification.ConflictSkip,
			Recipients: recipients,
		})
	}
	return nil
}
