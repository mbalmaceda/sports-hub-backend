package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config del pool. Los valores no son configurables por entorno a propósito:
// dependen de la forma del despliegue (una máquina chica de Fly contra un
// Postgres administrado en otra región), no de quién lo corre.
const (
	// El default de pgx es max(4, NumCPU), o sea 4 en la máquina compartida de
	// un vCPU. Fly admite ~25 conexiones concurrentes por máquina, así que con 4
	// las requests se quedan esperando un slot del pool aunque la base esté
	// ociosa. Tampoco conviene mucho más: cada conexión consume memoria del lado
	// del servidor y acá hay 256 MB.
	maxConns = 10

	// Ninguna conexión retenida, y es lo que más plata ahorra de todo este
	// archivo. Neon cobra el tiempo que el compute está despierto y lo suspende
	// recién tras cinco minutos sin actividad; con un mínimo de dos, el pool
	// soltaba las ociosas y el health check las volvía a abrir al minuto
	// siguiente, y cada reconexión reiniciaba ese reloj. La base quedaba
	// encendida las veinticuatro horas: unos US$50 al mes con el tráfico en cero.
	//
	// El costo es el handshake TLS en la primera request después de un rato, que
	// igual queda tapado por el arranque en frío de Neon; el corte de 15 s del
	// api-client de la app ya contempla esa espera.
	minConns = 0

	// Las conexiones se reciclan aunque estén sanas: del otro lado hay un pooler
	// que rota los backends, y una conexión eterna termina pegada a un backend
	// que ya no es el mejor. El jitter evita que se caigan todas juntas y
	// provoquen justo el pico que se quiere evitar.
	maxConnLifetime       = 30 * time.Minute
	maxConnLifetimeJitter = 5 * time.Minute

	// Lo ocioso se suelta enseguida: una conexión abierta sin uso no deja que
	// Neon cuente sus cinco minutos de inactividad, y además, al suspender, Neon
	// corta lo que quedó abierto. Soltarlo antes evita las dos cosas, la cuenta
	// estirada y descubrir la conexión muerta con una request en la mano.
	maxConnIdleTime = 1 * time.Minute

	// Cada cuánto el pool revisa lo ocioso. Con minConns en 0 no repone nada:
	// solo cierra.
	healthCheckPeriod = 1 * time.Minute

	// Cota al arranque: sin esto, una base inalcanzable deja el proceso colgado
	// en vez de fallar y dejar que Fly reintente.
	connectTimeout = 10 * time.Second
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	cfg.MaxConns = maxConns
	cfg.MinConns = minConns
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnLifetimeJitter = maxConnLifetimeJitter
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.HealthCheckPeriod = healthCheckPeriod
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
