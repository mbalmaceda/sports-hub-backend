/*
Package jobs son las cosas que pasan porque pasó el tiempo, no porque alguien
las hizo.

Las dieciocho notificaciones se dividen en dos por este eje. Quince nacen de una
acción —alguien desafía, alguien convoca, alguien paga— y por eso se emiten una
sola vez sin que nadie tenga que cuidar nada: la acción ocurre una vez. Las tres
que viven acá las dispara el reloj, y eso cambia todo el problema.

**El horario no decide qué se manda.** Vale la pena insistir en esto justamente
porque hay un scheduler de verdad y es fácil suponer lo contrario. Un cron no
tiene memoria: si la máquina de Fly se reinicia a las 08:59, la corrida de las
09:00 puede quedar sin hacer o hacerse dos veces según cómo caiga el arranque, y
`robfig/cron` —como cualquier scheduler en proceso— no guarda en ningún lado qué
corrió. Lo que garantiza que un aviso salga una sola vez es `dedupe_key`, que
vive en Postgres: el segundo intento no escribe fila y por lo tanto tampoco
genera push.

El horario decide **cuándo se revisa**, y ahí sí importa que sea un cron y no un
ticker: el aviso de una cuota impaga no puede salir a las tres de la mañana, y el
resumen del mes tiene que caer el día 1 y no en las noventa y seis pasadas de ese
día. Con expresiones, cada trabajo pide la cadencia que le corresponde en vez de
compartir un intervalo y filtrar por dentro.

Acá vive además la barrida de lo vencido, que hasta ahora corría dentro de cada
GET. Sacarla del camino de lectura era lo que este paquete venía esperando.
*/
package jobs

import (
	"context"
	"log/slog"
	"time"
	/*
		La base de datos de zonas horarias, embebida en el binario.

		Sin esto `LoadLocation("America/Santiago")` depende de que la imagen
		traiga tzdata, y `debian:bookworm-slim` —la que usa el Dockerfile— no lo
		trae. El resultado sería que el cron cae a UTC y los avisos de las 9 de
		la mañana salen a las 5, todos los días, sin que nadie lo note por
		semanas.

		Va embebida y no como paquete del sistema porque así no se puede
		desincronizar de lo que el código necesita: cambiar la imagen base no
		puede romperlo. Cuesta unos 450 KB en el binario.
	*/
	_ "time/tzdata"

	"github.com/robfig/cron/v3"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

/*
schedulerZone es la zona en la que se leen las expresiones.

Sin esto el cron corre en la del proceso, que en Fly es UTC: "todos los días a
las 9" saldría a las 5 de la mañana en Chile. Es el mismo cuidado que ya obligó a
que el recordatorio de partido no diga "mañana" en su texto —el servidor no está
donde está la gente— solo que acá sí se puede arreglar, porque la app es de un
país.

Si algún día hay equipos fuera de Chile, esto pasa a ser un dato del equipo y los
avisos con hora fija hay que agruparlos por zona.
*/
const schedulerZone = "America/Santiago"

const (
	/*
		reminderWindow es cuánta anticipación tiene el recordatorio del partido.

		Un día: suficiente para que el que no contestó pueda avisar que no llega
		y el manager salga a buscar un reemplazo. Más sería recordar algo que la
		persona todavía no tiene decidido.
	*/
	reminderWindow = 24 * time.Hour

	/*
		reminderGrace es cuánto tiene que haber pasado desde la citación para
		que valga la pena recordarla.

		Sin esto, a quien lo convocan el sábado a la mañana para el sábado a la
		tarde le llega "te convocaron" y, quince minutos después, el recordatorio
		de que no contestó. Tres horas es el margen para que responda en paz.
	*/
	reminderGrace = 3 * time.Hour

	// jobTimeout corta un trabajo que se cuelga. Ninguno tarda más que unos
	// segundos; el corte existe para que una consulta trabada no deje su
	// goroutine viva hasta el próximo reinicio.
	jobTimeout = 2 * time.Minute
)

// Deps son los repositorios que los trabajos necesitan. Va como struct y no como
// lista de parámetros porque son siete y crecen: una firma posicional de siete
// repositorios es una invitación a cruzarlos.
type Deps struct {
	Friendlies    friendly.Repository
	Competitions  competition.Repository
	Matches       match.Repository
	Fees          fee.Repository
	Teams         team.Repository
	Memberships   membership.Repository
	Notifications *notify.Service
}

type job struct {
	name string
	// spec es la expresión cron, leída en `schedulerZone`.
	spec string
	run  func(context.Context, Deps, time.Time) error
}

/*
Los cuatro trabajos y por qué cada uno corre cuando corre.

Las cadencias son distintas a propósito, y esa es la razón de tener un cron y no
un ticker compartido: dos de estos avisos le llegan a una persona a una hora
concreta, y los otros dos no le llegan a nadie.
*/
var all = []job{
	/*
		La barrida no le avisa a nadie: solo pone al día el estado en Postgres.
		Corre seguido porque es barata y porque su valor es que la base no mienta
		—un reporte o una consulta a mano tienen que ver lo mismo que la app—.
	*/
	{"sweep-expired", "*/10 * * * *", sweepExpired},
	/*
		El recordatorio mira una ventana móvil de veinticuatro horas, así que la
		frecuencia es su latencia: un partido que entra en la ventana se avisa
		dentro del cuarto de hora siguiente. No tiene hora fija porque los
		partidos tampoco: uno de las 8 de la mañana se recuerda la noche anterior.
	*/
	{"remind-matches", "*/15 * * * *", remindUpcomingMatches},
	/*
		La cuota vencida sí es un aviso con hora, y por eso va una vez al día a
		las 9: decirle a alguien a las tres de la mañana que un jugador no pagó es
		la forma más rápida de que apague las notificaciones. Una vez al día
		alcanza porque una deuda no cambia de estado en horas.
	*/
	{"overdue-fees", "0 9 * * *", announceOverdueFees},
	/*
		El resumen abre el mes: el día 1 a las 9. Con un ticker esto era un
		`if day != 1` adentro del trabajo y noventa y seis pasadas que leían todos
		los planteles para no emitir nada; acá es la expresión.
	*/
	{"monthly-summary", "0 9 1 * *", announceMonthlySummary},
}

/*
Start arranca los trabajos periódicos y devuelve la función para detenerlos.

Corre en la misma máquina y sin coordinación, igual que `StartTokenReaper`,
porque en Fly hay una sola. Con varias, lo peor que pasa es que dos instancias
hagan el mismo trabajo a la misma hora: la barrida es idempotente y las
notificaciones chocan contra el índice único, así que la segunda no escribe nada.

La barrida se corre además una vez al arrancar. Un despliegue es justamente
cuando conviene poner el estado al día, y esperar al próximo múltiplo de diez
minutos dejaría ese rato con datos viejos. Los otros tres no: dispararlos en cada
despliegue sería mandar el recordatorio a deshora.
*/
func Start(ctx context.Context, deps Deps, logger *slog.Logger) func() {
	location, err := time.LoadLocation(schedulerZone)
	if err != nil {
		// Sin base de datos de zonas horarias el cron correría en UTC y los
		// avisos con hora fija saldrían de madrugada. Se sigue igual —es peor no
		// tener trabajos periódicos que tenerlos corridos— pero queda anotado.
		logger.Error("could not load the scheduler time zone, falling back to UTC",
			"zone", schedulerZone, "error", err)
		location = time.UTC
	}

	c := cron.New(cron.WithLocation(location))

	// El EntryID de cada trabajo, para poder preguntarle al cron cuándo vuelve a
	// correr. Se guarda en vez de descartarlo porque `Entry(id).Next` es la
	// única forma de confirmar desde afuera que la expresión se leyó como
	// queríamos: "0 9 * * *" mal interpretada no falla al registrarse, falla
	// nueve horas después y en silencio.
	ids := make(map[string]cron.EntryID, len(all))
	for _, j := range all {
		id, err := c.AddFunc(j.spec, runner(ctx, deps, logger, j))
		if err != nil {
			// Una expresión mal escrita es un error de programación, no de
			// entorno: se registra y los demás trabajos arrancan igual.
			logger.Error("invalid cron expression", "job", j.name, "spec", j.spec, "error", err)
			continue
		}
		ids[j.name] = id
	}
	c.Start()

	/*
		El inventario de lo que quedó agendado, con la hora real de la próxima
		corrida de cada uno.

		Va después de `Start` y no adentro del loop de arriba porque `Next` recién
		queda poblado cuando el cron arranca; consultado antes devuelve el cero de
		`time.Time` y el registro diría "0001-01-01" para los cuatro.

		Sin estas líneas, un arranque sano y uno donde el scheduler no levantó se
		ven idénticos en los logs, y la diferencia recién aparece cuando alguien
		nota que no le llegó un aviso.
	*/
	for _, j := range all {
		id, ok := ids[j.name]
		if !ok {
			continue
		}
		logger.Info("scheduled job registered",
			"job", j.name,
			"spec", j.spec,
			"next_run", c.Entry(id).Next.In(location).Format(time.RFC3339))
	}
	logger.Info("scheduler started", "zone", location.String(), "jobs", len(ids))

	go runOnce(ctx, deps, logger, all[0], triggerStartup)

	return func() {
		logger.Info("scheduler stopping")
		// Stop espera a que terminen las corridas en curso, que es lo que hace
		// falta para no cortar una barrida a la mitad al apagar el servidor.
		<-c.Stop().Done()
		logger.Info("scheduler stopped")
	}
}

/*
Qué disparó una corrida.

La barrida es el único trabajo que corre por dos motivos —el reloj y el arranque
del proceso, ver `Start`— y en los logs son dos hechos distintos: un
`sweep-expired` de arranque a las 09:03 es un despliegue, uno agendado a las 09:03
no existe. Sin esta distinción, una máquina reiniciándose en loop parece un cron
sano corriendo seguido.
*/
const (
	triggerSchedule = "schedule"
	triggerStartup  = "startup"
)

func runner(ctx context.Context, deps Deps, logger *slog.Logger, j job) func() {
	return func() { runOnce(ctx, deps, logger, j, triggerSchedule) }
}

// runOnce corre un trabajo con su propio corte de tiempo.
//
// El error se registra y no se propaga: no hay a quién devolvérselo, y un fallo
// no puede impedir la próxima corrida. Reintentar tampoco hace falta —la próxima
// pasada vuelve a encontrar lo mismo— y esa es otra cosa que la deduplicación
// vuelve gratis: reintentar no puede duplicar nada.
//
// Las dos líneas que enmarcan la corrida son lo que vuelve legible el flujo desde
// afuera. La de apertura existe para el caso en que no haya cierre: si la máquina
// se queda sin memoria a mitad de una barrida, el `started` sin su `finished` es
// el único rastro que queda de qué estaba haciendo el proceso cuando murió. La de
// cierre lleva la duración porque un trabajo que empieza a tardar de más es lo
// que avisa que una consulta dejó de usar un índice, mucho antes de que empiece a
// chocar contra `jobTimeout`.
func runOnce(ctx context.Context, deps Deps, logger *slog.Logger, j job, trigger string) {
	runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	logger.Info("scheduled job started", "job", j.name, "trigger", trigger)
	start := time.Now()

	if err := j.run(runCtx, deps, start); err != nil {
		logger.Error("scheduled job failed",
			"job", j.name,
			"trigger", trigger,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", err)
		return
	}

	logger.Info("scheduled job finished",
		"job", j.name,
		"trigger", trigger,
		"duration_ms", time.Since(start).Milliseconds())
}
