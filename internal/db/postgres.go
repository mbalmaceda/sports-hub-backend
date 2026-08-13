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

	// Un par de conexiones tibias. Abrir una nueva contra Postgres administrado
	// cuesta el handshake TLS completo, decenas de milisegundos que se le cargan
	// a la primera request; con auto_stop_machines eso pasa cada vez que la
	// máquina despierta.
	minConns = 2

	// Las conexiones se reciclan aunque estén sanas: del otro lado hay un pooler
	// que rota los backends, y una conexión eterna termina pegada a un backend
	// que ya no es el mejor. El jitter evita que se caigan todas juntas y
	// provoquen justo el pico que se quiere evitar.
	maxConnLifetime       = 30 * time.Minute
	maxConnLifetimeJitter = 5 * time.Minute

	// Postgres administrado suspende la instancia cuando no hay tráfico y corta
	// lo que quedó abierto. Soltar lo ocioso antes evita descubrirlo con una
	// conexión muerta en la mano.
	maxConnIdleTime = 5 * time.Minute

	// Cada cuánto el pool revisa lo ocioso y repone hasta minConns.
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
