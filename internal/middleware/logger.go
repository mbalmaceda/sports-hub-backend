package middleware

import (
	"bytes"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
)

// maxLoggedBodyBytes limita cuánto del cuerpo de respuesta se captura para el
// log de errores. El cuerpo de un error es un JSON corto —"max_uses is too
// high"—; si llega a ser grande, el resto sobra.
const maxLoggedBodyBytes = 512

// Logger reemplaza a gin.Logger(), que escribe texto plano mientras el resto de
// la app escribe JSON con slog: dos formatos en el mismo stream son imposibles
// de consultar o alertar en Fly.
//
// Loguea la ruta declarada (c.FullPath(), o sea "/teams/:id") y no la pedida,
// porque la pedida trae ids que son datos personales y además hacen que cada
// request sea una serie distinta al agrupar.
//
// En un 4xx/5xx también loguea el cuerpo de la respuesta: los handlers
// devuelven el mensaje de error en el JSON pero no siempre lo adjuntan a
// c.Errors, así que sin esto un 400 en producción es solo un número sin
// contexto.
func Logger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		body := newBodyCapture()
		c.Writer = &bodyCaptureWriter{ResponseWriter: c.Writer, body: body}
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
		if status := c.Writer.Status(); status >= 400 && body.len() > 0 {
			attrs = append(attrs, "response", body.string())
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

// bodyCaptureWriter envuelve al writer de Gin y va guardando lo que escribe el
// handler, para que el log pueda decir qué error salió. Delega todo lo demás en
// el writer original.
type bodyCaptureWriter struct {
	gin.ResponseWriter
	body *bodyCapture
}

func (w *bodyCaptureWriter) Write(b []byte) (int, error) {
	w.body.capture(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyCaptureWriter) WriteString(s string) (int, error) {
	w.body.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// bodyCapture acumula hasta maxLoggedBodyBytes del cuerpo de respuesta. Una vez
// que se llenó, deja de copiar: lo que importa del mensaje ya se capturó.
type bodyCapture struct {
	limit     int
	truncated bool
	buf       bytes.Buffer
}

func newBodyCapture() *bodyCapture {
	return &bodyCapture{limit: maxLoggedBodyBytes}
}

func (c *bodyCapture) capture(b []byte) {
	if c.truncated {
		return
	}
	remaining := c.limit - c.buf.Len()
	switch {
	case remaining <= 0:
		c.truncated = true
	case len(b) > remaining:
		c.buf.Write(b[:remaining])
		c.truncated = true
	default:
		c.buf.Write(b)
	}
}

func (c *bodyCapture) len() int { return c.buf.Len() }

func (c *bodyCapture) string() string {
	if c.truncated {
		return c.buf.String() + "..."
	}
	return c.buf.String()
}
