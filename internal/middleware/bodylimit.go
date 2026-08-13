package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// BodyLimit corta la lectura del cuerpo en maxBytes.
//
// ShouldBindJSON lee hasta que se acaba el body, así que hoy un POST de 500 MB
// se copia entero a memoria. La máquina de Fly tiene 256 MB: alcanza un request
// para matar el proceso.
//
// MaxBytesReader hace que el error salga al leer, no al recibir, así que el
// handler recibe un fallo de binding común y el cliente se lleva un 400. Para
// que se lleve un 413, que es lo correcto, hay que mirar el Content-Length
// declarado antes de tocar el cuerpo.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		// Un cliente puede omitir o mentir el Content-Length (chunked), así que
		// el límite real lo pone igual el reader.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
