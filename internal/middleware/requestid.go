// Package middleware agrupa el borde HTTP: lo que se aplica a toda request
// antes de llegar a un handler. La autenticación no vive acá —está en
// internal/auth— porque cuelga de un grupo de rutas y no del borde.
package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

const (
	requestIDKey    = "request_id"
	requestIDHeader = "X-Request-Id"
)

// RequestID le pone un identificador a cada request y lo devuelve en la
// respuesta, para poder seguir un incidente reportado por un usuario hasta sus
// líneas de log.
//
// El id que manda el cliente no se reutiliza: viene de afuera, así que podría
// repetirse a propósito para ensuciar la correlación de dos requests distintas.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := newRequestID()
		c.Set(requestIDKey, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// RequestIDFromContext devuelve el id de la request en curso, o "" si el
// middleware no está montado.
func RequestIDFromContext(c *gin.Context) string {
	id, ok := c.Get(requestIDKey)
	if !ok {
		return ""
	}
	s, _ := id.(string)
	return s
}

func newRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand no falla en la práctica, y si fallara tampoco vale la pena
		// tirar la request abajo: un id vacío degrada el log, nada más.
		return ""
	}
	return hex.EncodeToString(raw)
}
