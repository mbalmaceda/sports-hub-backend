package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

/*
La página de invitación se sirve con la misma CSP que la API, y esa no la deja
mostrarse.

`SecureHeaders` manda `default-src 'none'`, que bloquea el <style> inline: el
navegador tira la hoja y la invitación aparece como HTML sin formato. El detalle
que hace que valga un test es que **no se ve con curl** —los clientes de línea
de comandos ignoran la CSP—, así que la regresión solo aparece en un teléfono, y
tarde.

Se prueba con el middleware puesto, que es como corre en producción: probar el
handler solo dejaría pasar exactamente el bug que esto cuida.
*/
func TestInvitePage_AllowsItsOwnStyles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	invites := &testutil.MockGuestInviteRepo{}
	invites.On("FindByTokenHash", mock.Anything, mock.Anything).Return(openInvite(), nil)

	h := handler.NewAppLinksHandler(invites, config.Config{PublicBaseURL: "https://zports.test"})

	r := gin.New()
	r.Use(middleware.SecureHeaders())
	r.GET("/i/:token", h.InvitePage)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i/un-token", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	csp := w.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'unsafe-inline'", "la página no puede pintar sus estilos")

	// Lo que la CSP tiene que seguir prohibiendo. La página no ejecuta nada ni
	// trae nada de afuera, y aflojar esto la convertiría en una superficie que
	// hoy no es: es el único HTML público de la app.
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.NotContains(t, csp, "script-src")

	// Y que efectivamente haya estilos que mostrar: sin esto el test pasaría
	// igual el día que la página deje de tener <style> y nadie se enteraría de
	// que la CSP quedó permitiendo algo que ya no hace falta.
	assert.True(t, strings.Contains(w.Body.String(), "<style>"))
}
