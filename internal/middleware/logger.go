package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
)

// Logger reemplaza a gin.Logger(), que escribe texto plano mientras el resto de
// la app escribe JSON con slog: dos formatos en el mismo stream son imposibles
// de consultar o alertar en Fly.
//
// Loguea la ruta declarada (c.FullPath(), o sea "/teams/:id") y no la pedida,
// porque la pedida trae ids que son datos personales y además hacen que cada
// request sea una serie distinta al agrupar.
func Logger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		attrs := []any{
			"request_id", RequestIDFromContext(c),
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		}
		// El user_id solo existe si el middleware de auth ya corrió y validó el
		// token; en las rutas públicas simplemente no está.
		if claims, ok := auth.ClaimsFromContext(c); ok {
			attrs = append(attrs, "user_id", claims.UserID)
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}

		// Un 5xx es un problema nuestro y merece nivel error; un 4xx es el
		// cliente haciendo algo mal y no debería despertar a nadie.
		switch {
		case c.Writer.Status() >= 500:
			logger.Error("request", attrs...)
		case c.Writer.Status() >= 400:
			logger.Warn("request", attrs...)
		default:
			logger.Info("request", attrs...)
		}
	}
}
