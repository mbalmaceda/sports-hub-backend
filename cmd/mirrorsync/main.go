// Command mirrorsync vuelve a escribir en Firestore el espejo de membresías
// entero, tomando Postgres como fuente de verdad.
//
// El espejo se mantiene solo: cada alta, cambio de rol o baja lo actualiza. Esto
// existe para los casos en que se desfasa igual — Firestore caído durante un
// cambio, una migración que tocó la base por fuera de la API, o el arranque,
// cuando hay membresías anteriores a que el espejo existiera.
//
// Es idempotente: correrlo dos veces deja lo mismo que correrlo una.
//
//	go run ./cmd/mirrorsync            # muestra qué haría
//	go run ./cmd/mirrorsync --apply    # escribe
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
)

func main() {
	config.LoadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	fb, err := firebase.New(ctx, cfg.FirebaseServiceAccount)
	if err != nil {
		slog.Error("firebase error", "error", err)
		os.Exit(1)
	}
	if !fb.Enabled() {
		slog.Error("no hay FIREBASE_SERVICE_ACCOUNT: no hay nada que sincronizar")
		os.Exit(1)
	}
	defer fb.Close()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database error", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// El match_id del invitado sale de su convocatoria, que es lo que lo ata a
	// un partido. Si tuviera más de una —hoy no pasa, el canje crea una sola—
	// gana la última: es la que corresponde al enlace más reciente.
	rows, err := pool.Query(ctx, `
		SELECT m.team_id, m.user_id, m.role, m.status, m.kind,
		       COALESCE((
		           SELECT cu.match_id::text FROM match_callups cu
		           WHERE cu.membership_id = m.id
		           ORDER BY cu.called_at DESC
		           LIMIT 1
		       ), '')
		FROM memberships m`)
	if err != nil {
		slog.Error("query error", "error", err)
		os.Exit(1)
	}
	defer rows.Close()

	var memberships []firebase.Membership
	for rows.Next() {
		var m firebase.Membership
		var matchID string
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Role, &m.Status, &m.Kind, &matchID); err != nil {
			slog.Error("scan error", "error", err)
			os.Exit(1)
		}
		// Solo el invitado queda acotado a un partido; el plantel los ve todos.
		if m.Kind == "guest" {
			m.MatchID = matchID
		}
		memberships = append(memberships, m)
	}
	if err := rows.Err(); err != nil {
		slog.Error("rows error", "error", err)
		os.Exit(1)
	}

	apply := len(os.Args) > 1 && os.Args[1] == "--apply"
	if !apply {
		fmt.Printf("%d membresías se escribirían en Firestore. Pasar --apply para hacerlo.\n", len(memberships))
		return
	}

	// De a una y en serie: son pocas y esto se corre a mano. Paralelizarlo
	// arriesgaría toparse con los límites de escritura de Firestore sin ganar
	// nada apreciable.
	var failed int
	for _, m := range memberships {
		if err := fb.SyncMembership(ctx, m); err != nil {
			slog.Error("sync failed", "error", err, "team_id", m.TeamID, "user_id", m.UserID)
			failed++
		}
	}

	fmt.Printf("sincronizadas %d de %d\n", len(memberships)-failed, len(memberships))
	if failed > 0 {
		os.Exit(1)
	}
}
