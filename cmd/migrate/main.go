package main

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/joho/godotenv"

	sportsmigrations "github.com/mbalmaceda/sports-hub-backend/migrations"
)

func main() {
	_ = godotenv.Load() // carga .env en desarrollo, ignorado en prod

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
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
	default:
		slog.Error("unknown direction, use 'up' or 'down'", "got", direction)
		os.Exit(1)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		slog.Error("migration failed", "direction", direction, "error", err)
		os.Exit(1)
	}
	slog.Info("done", "direction", direction)
}
