package auth

import (
	"context"
	"log/slog"
	"time"
)

const (
	reaperInterval = 6 * time.Hour
	// reaperGrace deja las filas vencidas un rato más antes de borrarlas: si
	// alguien reporta que lo desloguearon, el rastro de esa sesión —incluida la
	// marca de revocación por reutilización— todavía está en la tabla.
	reaperGrace = 7 * 24 * time.Hour
)

// StartTokenReaper borra periódicamente los refresh tokens vencidos.
//
// Hasta ahora nada limpiaba la tabla: quedaba una fila por login, para siempre.
// Corre en la misma máquina y sin coordinación porque en Fly hay una sola; con
// varias, el DELETE es idempotente y lo peor que pasa es que se pisen.
//
// Termina cuando se cancela el contexto, o sea junto con el servidor.
func StartTokenReaper(ctx context.Context, tokens RefreshTokenRepository, logger *slog.Logger) {
	go func() {
		ticker := time.NewTicker(reaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := tokens.DeleteExpired(ctx, time.Now().Add(-reaperGrace))
				if err != nil {
					logger.Error("refresh token reaper failed", "error", err)
					continue
				}
				if deleted > 0 {
					logger.Info("refresh tokens reaped", "deleted", deleted)
				}
			}
		}
	}()
}
