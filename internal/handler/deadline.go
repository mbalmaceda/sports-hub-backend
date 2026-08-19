package handler

import "time"

/*
Hasta cuándo se puede responder algo que se juega en una fecha.

El plazo normal es el TTL —dos días para un amistoso, una semana para un
torneo—, pero **nunca puede pasar la hora del evento**. Un amistoso para esta
tarde tiene de plazo esta tarde, no pasado mañana: aceptar un partido después de
que se jugó no significa nada, y mientras tanto la app muestra un contador que
promete más tiempo del que existe.

Sin este techo, el desafío para hoy a las 18:00 nacía con vencimiento a 48
horas. El rival veía "1d 20h para responder" sobre un partido que empezaba en
cuatro, y si aceptaba al día siguiente se creaba un partido con fecha pasada.

Es la misma regla que ya usa el enlace de invitados, que vence a la hora del
partido (`guest_handler.go`). Acá se escribe una vez porque son tres los lugares
que la necesitan —crear un amistoso, contraofertar e invitar a un torneo— y los
tres tienen que decir lo mismo.

Sin fecha de evento no hay techo que aplicar y manda el TTL: es el caso del
torneo que todavía no tiene calendario.
*/
func responseDeadline(now time.Time, ttl time.Duration, eventAt *time.Time) time.Time {
	deadline := now.Add(ttl)
	if eventAt != nil && eventAt.Before(deadline) {
		return *eventAt
	}
	return deadline
}
