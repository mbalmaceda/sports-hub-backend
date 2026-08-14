package main

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"

	sportsmigrations "github.com/mbalmaceda/sports-hub-backend/migrations"
)

func main() {
	config.LoadDotEnv()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}

	// Las migraciones no van por el pooler.
	//
	// golang-migrate se serializa con pg_advisory_lock, que es un lock de
	// sesión. Un pooler en modo transacción devuelve la conexión al pool entre
	// sentencias, así que el lock puede quedar tomado en un backend distinto
	// del que después lo libera: dos migraciones simultáneas dejan de excluirse,
	// que es justo lo único que ese lock tenía que garantizar.
	//
	// Es un aviso y no un error porque el endpoint directo se llama distinto en
	// cada proveedor y adivinarlo sería peor que avisar.
	if strings.Contains(dbURL, "-pooler.") {
		slog.Warn("DATABASE_URL apunta al pooler; las migraciones deberían ir al endpoint directo " +
			"(el mismo host sin '-pooler') porque el lock de golang-migrate es de sesión")
	}

	// golang-migrate pgx v5 driver requiere scheme "pgx5://"
	migrateURL := strings.NewReplacer(
		"postgresql://", "pgx5://",
		"postgres://", "pgx5://",
	).Replace(dbURL)

	src, err := iofs.New(sportsmigrations.FS, ".")
	if err != nil {
		slog.Error("failed to load migrations", "error", err)
		os.Exit(1)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL)
	if err != nil {
		slog.Error("failed to create migrator", "error", err)
		os.Exit(1)
	}
	defer m.Close()

	switch direction {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	case "drop":
		// Borra TODO, incluida la tabla de versiones: deja la base como recién
		// creada. Es la única forma de volver a aplicar el esquema base sobre
		// una base que ya tiene una versión registrada.
		//
		// Pide el opt-in explícito por lo obvio: escrito sin querer contra el
		// DATABASE_URL equivocado, no hay nada que deshacer.
		if os.Getenv("ALLOW_DROP") != "true" {
			slog.Error("drop deshabilitado: exporta ALLOW_DROP=true y verifica a qué base apunta DATABASE_URL")
			os.Exit(1)
		}
		err = m.Drop()
	default:
		slog.Error("unknown command, use 'up', 'down' or 'drop'", "got", direction)
		os.Exit(1)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		slog.Error("migration failed", "direction", direction, "error", err)
		os.Exit(1)
	}
	slog.Info("done", "direction", direction)
}
