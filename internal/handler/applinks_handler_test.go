package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	h := handler.NewAppLinksHandler(invites, &testutil.MockTeamLinkRepo{}, config.Config{PublicBaseURL: "https://zports.test"})

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

// La página del enlace al equipo abre la app en `join/`, la pantalla del enlace
// al equipo, y no en `invite/`, que es la del partido: con la equivocada, quien
// tiene la app vería "esta invitación no existe".
func TestTeamLinkPage_OpensTheJoinScreen(t *testing.T) {
	gin.SetMode(gin.TestMode)

	links := &testutil.MockTeamLinkRepo{}
	links.On("FindByTokenHash", mock.Anything, hashFor("tok")).Return(openTeamLink(), nil)

	h := handler.NewAppLinksHandler(&testutil.MockGuestInviteRepo{}, links, config.Config{PublicBaseURL: "https://zports.test"})

	r := gin.New()
	r.GET("/e/:token", h.TeamLinkPage)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e/tok", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "zports://join/tok")
}

func TestTeamLinkPage_ExpiredIsGone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	expired := openTeamLink()
	expired.ExpiresAt = expired.CreatedAt.Add(-time.Hour)
	links := &testutil.MockTeamLinkRepo{}
	links.On("FindByTokenHash", mock.Anything, mock.Anything).Return(expired, nil)

	h := handler.NewAppLinksHandler(&testutil.MockGuestInviteRepo{}, links, config.Config{})

	r := gin.New()
	r.GET("/e/:token", h.TeamLinkPage)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e/tok", nil))

	assert.Equal(t, http.StatusGone, w.Code)
	assert.NotContains(t, w.Body.String(), "zports://")
}
