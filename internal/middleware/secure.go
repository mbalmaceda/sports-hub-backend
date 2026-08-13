package middleware

import "github.com/gin-gonic/gin"

// SecureHeaders agrega las cabeceras de seguridad que corresponden a una API
// que solo devuelve JSON.
//
// Nada de esto protege a la app móvil, que ignora las cabeceras: son
// instrucciones para el navegador, y existen porque el frontend web pega contra
// esta misma API y porque una respuesta nuestra no debería poder embeberse ni
// interpretarse como otra cosa.
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		// El navegador no adivina el tipo: si decimos JSON, es JSON. Cierra los
		// ataques que hacen pasar una respuesta por script o HTML.
		h.Set("X-Content-Type-Options", "nosniff")
		// Ninguna respuesta de esta API tiene por qué vivir dentro de un frame.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		// Que la URL de la API no viaje como referer hacia terceros: los paths
		// llevan ids de equipos, partidos y personas.
		h.Set("Referrer-Policy", "no-referrer")
		// Fly ya fuerza HTTPS (force_https en fly.toml); esto evita además el
		// primer request en claro de un cliente que llegue por http://.
		//
		// Sin `preload` a propósito: .fly.dev es un dominio compartido y no nos
		// corresponde inscribirlo en la lista de precarga de los navegadores.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}
