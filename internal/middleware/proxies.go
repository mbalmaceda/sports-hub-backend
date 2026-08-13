package middleware

import "github.com/gin-gonic/gin"

// ConfigureTrustedProxies deja el router preparado para vivir detrás del proxy
// de Fly y de ningún otro.
//
// Por defecto Gin confía en todos los proxies, así que ClientIP() sale de
// X-Forwarded-For: un header que escribe el cliente. Cualquier límite por IP se
// saltaría rotando ese valor. Fly-Client-IP, en cambio, lo reescribe el edge de
// Fly en cada request y no se puede inyectar desde afuera.
//
// TrustedPlatform tiene prioridad sobre los proxies confiables dentro de Gin, y
// la lista vacía hace que X-Forwarded-For se ignore por completo. Fuera de Fly
// —en local— no llega ese header y ClientIP() cae a la dirección real del
// socket, que es lo correcto.
//
// Vive acá y no suelto en main para que el comportamiento tenga un test: la
// alerta "You trusted all proxies" que Gin imprimía solo aparece al usar
// r.Run(), y desde que el servidor es un http.Server explícito su ausencia ya
// no prueba nada.
func ConfigureTrustedProxies(r *gin.Engine) error {
	if err := r.SetTrustedProxies(nil); err != nil {
		return err
	}
	r.TrustedPlatform = gin.PlatformFlyIO
	return nil
}
