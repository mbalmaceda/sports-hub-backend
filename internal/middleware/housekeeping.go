package middleware

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// housekeepingTimeout corta una tarea que se cuelga, igual que el `jobTimeout`
// del scheduler: corre en su propia goroutine y nadie la espera.
const housekeepingTimeout = 2 * time.Minute

// Task es una tarea de mantenimiento y cada cuánto, como mucho, puede correr.
type Task struct {
	Every time.Duration
	Run   func(context.Context)
}

/*
Housekeeping corre tareas de mantenimiento con el tráfico en vez de con un reloj.

Es lo que reemplaza al scheduler cuando está apagado (`JOBS_ENABLED`, ver
`config`). Neon cobra el tiempo que la base pasa despierta, y un ticker que la toca
cada pocos minutos la deja encendida todo el día aunque nadie use la app. Colgadas
de las requests, la barrida y la limpieza de tokens solo corren cuando la base ya
está despierta por otra razón, y no le agregan horas a la cuenta.

Cada tarea corre a lo más una vez por `Every` y por proceso: el
`CompareAndSwap` hace que, de varias requests simultáneas, una sola se quede con
la corrida. Nunca bloquea la request: corre en una goroutine con su propio
contexto, porque el de la request se cancela apenas se responde.

Va en el grupo con sesión y no en todo el router a propósito: Fly pega contra
/health cada treinta segundos, y engancharla ahí volvería a despertar la base
sola, que es justo lo que esto existe para evitar.
*/
func Housekeeping(tasks ...Task) gin.HandlerFunc {
	last := make([]atomic.Int64, len(tasks))
	return func(c *gin.Context) {
		now := time.Now().UnixNano()
		for i, t := range tasks {
			prev := last[i].Load()
			if now-prev < int64(t.Every) || !last[i].CompareAndSwap(prev, now) {
				continue
			}
			go func(run func(context.Context)) {
				ctx, cancel := context.WithTimeout(context.Background(), housekeepingTimeout)
				defer cancel()
				run(ctx)
			}(t.Run)
		}
		c.Next()
	}
}
