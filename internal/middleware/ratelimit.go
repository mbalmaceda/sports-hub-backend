package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
)

// maxTrackedKeys es el techo duro de claves vivas. El barrido por inactividad
// alcanza para el uso normal, pero sin un tope el propio limitador sería el
// agujero: bastaría pegar desde muchas IPs para llenar la memoria de la máquina.
const maxTrackedKeys = 50_000

// Limiter aplica un token bucket por clave, en memoria.
//
// En memoria es lo correcto hoy: Fly corre una sola máquina en una sola región.
// Si algún día hay N máquinas, el límite efectivo pasa a ser N veces el
// configurado y hay que mover el estado a Redis — la interfaz de acá no cambia.
type Limiter struct {
	limit rate.Limit
	burst int
	// idleTTL es cuánto sobrevive una clave sin uso. Tiene que ser mayor que la
	// ventana: si se olvidara antes, el atacante espera y arranca de cero.
	idleTTL time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket

	stop chan struct{}
	once sync.Once
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New arma un limitador de `requests` peticiones cada `per`, permitiendo esa
// misma cantidad de golpe y reponiendo de a una. Arranca el barrido de claves
// inactivas; hay que llamar a Stop al apagar.
func New(requests int, per time.Duration) *Limiter {
	l := &Limiter{
		limit:   rate.Limit(float64(requests) / per.Seconds()),
		burst:   requests,
		idleTTL: 3 * per,
		buckets: make(map[string]*bucket),
		stop:    make(chan struct{}),
	}
	go l.sweepLoop(per)
	return l
}

// Allow descuenta una petición para la clave. Si no hay cupo devuelve false y
// cuánto falta para que vuelva a haber, que es lo que se manda en Retry-After.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	res := l.bucketFor(key).Reserve()
	if !res.OK() {
		// Solo pasa si el burst es menor a lo pedido, que acá nunca ocurre.
		return false, time.Duration(float64(time.Second) / float64(l.limit))
	}
	if delay := res.Delay(); delay > 0 {
		// La reserva se cancela para no consumir cupo futuro por un rechazo:
		// si no, un cliente bloqueado se seguiría empujando su propia espera.
		res.Cancel()
		return false, delay
	}
	return true, 0
}

// Handle es el middleware para limitar por una clave sacada de la request.
// Si keyFn devuelve "" la request pasa sin contar: es preferible dejar entrar
// algo que no supimos identificar antes que rechazar a todo el mundo.
func (l *Limiter) Handle(keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)
		if key == "" {
			c.Next()
			return
		}
		if ok, retryAfter := l.Allow(key); !ok {
			AbortTooManyRequests(c, retryAfter)
			return
		}
		c.Next()
	}
}

// ByIP limita por IP del cliente. Depende de que el router tenga configurados
// los proxies confiables: sin eso ClientIP() sale de X-Forwarded-For, que
// cualquiera puede escribir, y el límite no limita nada.
func (l *Limiter) ByIP() gin.HandlerFunc {
	return l.Handle(func(c *gin.Context) string { return c.ClientIP() })
}

// ByUser limita por usuario autenticado. Va después del middleware de auth; si
// llegara sin claims cae a la IP, que es lo único identificable que queda.
func (l *Limiter) ByUser() gin.HandlerFunc {
	return l.Handle(func(c *gin.Context) string {
		if claims, ok := auth.ClaimsFromContext(c); ok {
			return claims.UserID
		}
		return c.ClientIP()
	})
}

// Stop corta el barrido. Es idempotente.
func (l *Limiter) Stop() {
	l.once.Do(func() { close(l.stop) })
}

func (l *Limiter) bucketFor(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	if b, ok := l.buckets[key]; ok {
		b.lastSeen = time.Now()
		return b.limiter
	}
	if len(l.buckets) >= maxTrackedKeys {
		l.evictLocked()
	}
	b := &bucket{limiter: rate.NewLimiter(l.limit, l.burst), lastSeen: time.Now()}
	l.buckets[key] = b
	return b.limiter
}

func (l *Limiter) sweepLoop(every time.Duration) {
	// Barrer más seguido que la ventana no aporta y despierta la máquina, que en
	// Fly se suspende sola cuando no hay tráfico.
	if every < time.Minute {
		every = time.Minute
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.sweep()
		case <-l.stop:
			return
		}
	}
}

func (l *Limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.idleTTL)
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// evictLocked libera espacio cuando se llegó al techo. Primero saca lo inactivo;
// si aun así está lleno —o sea, hay 50k claves activas de verdad— tira las que
// haya que tirar para poder seguir atendiendo. Se llama con el mutex tomado.
func (l *Limiter) evictLocked() {
	cutoff := time.Now().Add(-l.idleTTL)
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
	for key := range l.buckets {
		if len(l.buckets) < maxTrackedKeys {
			return
		}
		delete(l.buckets, key)
	}
}

// AbortTooManyRequests responde 429 con Retry-After. Vive acá para que el
// limitador y los handlers que llaman a Allow() den la misma respuesta.
func AbortTooManyRequests(c *gin.Context, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error": "too many requests, try again later",
	})
}
