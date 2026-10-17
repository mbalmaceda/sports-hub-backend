package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminTokenHeader es donde viaja la llave de servicio del admin.
const AdminTokenHeader = "X-Admin-Token"

/*
AdminToken protege `/admin` con una llave de servicio (`ADMIN_TOKEN`).

Es la puerta para cargar y corregir el catálogo con `curl` mientras no exista el
admin web. No es la auth del admin: cuando haya una web, lo que corresponde es
un usuario con permiso (`users.is_admin`) entrando con el JWT de siempre, y este
middleware se reemplaza por un `requireAdmin` sin tocar los handlers.

Sin el secreto configurado el grupo responde 404, no 401: apagado por defecto,
y sin anunciarle a nadie que ahí hay algo.

La comparación es en tiempo constante para que medir cuánto tarda la respuesta
no vaya revelando la llave de a un byte.
*/
func AdminToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		got := c.GetHeader(AdminTokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
