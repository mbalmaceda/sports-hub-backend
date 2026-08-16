package handler

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
)

/*
AppLinksHandler sirve las tres piezas que hacen que un enlace de invitación
compartido por WhatsApp funcione para alguien que no tiene la app.

Por qué vive en el backend y no en un sitio aparte: el destinatario de este
enlace es, por definición, alguien sin la app instalada. Un `zports://` en
WhatsApp no le abre nada y ni siquiera es tocable, así que el enlace tiene que
ser `https://` y algo tiene que responder ese `https://`. Levantar un sitio solo
para eso, con su dominio y su deploy, cuando ya hay un servidor corriendo, es
infraestructura de más para tres archivos.

Las tres piezas:

	GET /i/:token                            la página que ve la persona
	GET /.well-known/assetlinks.json         Android verifica y abre la app
	GET /.well-known/apple-app-site-association   iOS, lo mismo

Los dos archivos de verificación se sirven desde configuración y no desde el
repo porque llevan huellas de certificados de firma: cambian al rotar la clave
de Play, y con la app en dos entornos no son la misma.
*/
type AppLinksHandler struct {
	invites guest.Repository
	cfg     config.Config
}

func NewAppLinksHandler(invites guest.Repository, cfg config.Config) *AppLinksHandler {
	return &AppLinksHandler{invites: invites, cfg: cfg}
}

/*
AssetLinks GET /.well-known/assetlinks.json

Lo que hace que Android abra la app en vez del navegador. El sistema lo busca
solo, al instalar, y compara la huella del certificado con la de la app: si no
coincide —o si el archivo no está— el enlace abre Chrome y la invitación igual
se ve, solo que en la web.

Sin fingerprints configurados devuelve 404 en vez de un array vacío: un archivo
vacío le dice a Android "acá no hay ninguna app asociada" y queda cacheado.
*/
func (h *AppLinksHandler) AssetLinks(c *gin.Context) {
	if len(h.cfg.AndroidCertFingerprints) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "android app links are not configured"})
		return
	}

	c.JSON(http.StatusOK, []gin.H{{
		"relation": []string{"delegate_permission/common.handle_all_urls"},
		"target": gin.H{
			"namespace":                "android_app",
			"package_name":             h.cfg.AndroidPackageName,
			"sha256_cert_fingerprints": h.cfg.AndroidCertFingerprints,
		},
	}})
}

/*
AppleAppSiteAssociation GET /.well-known/apple-app-site-association

El equivalente de iOS. Va sin extensión y con Content-Type de JSON, que es como
lo pide Apple.

`paths` acota qué URLs abre la app: solo las invitaciones. Con "*" el sistema le
entregaría también la documentación o cualquier página futura de este dominio.
*/
func (h *AppLinksHandler) AppleAppSiteAssociation(c *gin.Context) {
	if h.cfg.AppleAppID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "ios universal links are not configured"})
		return
	}

	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, gin.H{
		"applinks": gin.H{
			"apps": []string{},
			"details": []gin.H{{
				"appID": h.cfg.AppleAppID,
				"paths": []string{"/i/*"},
			}},
		},
	})
}

/*
InvitePage GET /i/:token

La página que abre quien recibe el enlace.

Es HTML plano y sin JavaScript a propósito: se abre dentro del navegador de
WhatsApp, en un teléfono de gama baja, con la red que haya. Todo lo que sume
peso acá es gente que no llega a ver a qué la invitaron.

Con la app instalada, Android e iOS interceptan antes de que esto se pinte y la
abren directo. Esta página es lo que ve quien NO la tiene, y por eso su trabajo
es uno solo: contarle a qué lo invitan y ofrecerle la app.
*/
func (h *AppLinksHandler) InvitePage(c *gin.Context) {
	token := c.Param("token")

	inv, err := h.invites.FindByTokenHash(c.Request.Context(), hashInviteToken(token))
	if errors.Is(err, guest.ErrNotFound) {
		h.renderPage(c, http.StatusNotFound, invitePageData{
			Title: "Esta invitación no existe",
			Body:  "Puede que el enlace esté incompleto. Pídele uno nuevo a quien te invitó.",
		})
		return
	}
	if err != nil {
		h.renderPage(c, http.StatusInternalServerError, invitePageData{
			Title: "No pudimos abrir la invitación",
			Body:  "Vuelve a intentarlo en un momento.",
		})
		return
	}

	if !inv.Usable(time.Now()) {
		h.renderPage(c, http.StatusGone, invitePageData{
			Title: "Este enlace ya no sirve",
			Body:  "Se llenaron los lugares, lo anularon o el partido ya empezó. Pídele uno nuevo a quien te invitó.",
		})
		return
	}

	/*
		El enlace a la app va como template.URL y no como string.

		html/template sanea los href y solo deja pasar esquemas conocidos, así
		que `zports://` salía reemplazado por `#ZgotmplZ` y el botón "Ya tengo la
		app" no hacía nada. Es el respaldo de App Links —lo que salva al que
		tiene la app cuando la verificación no está configurada— así que muerto
		no sirve de nada.

		Marcarlo como vetado es seguro porque el token va escapado: PathEscape
		convierte comillas y ángulos en %22/%3C/%3E, de modo que lo que venga por
		la URL no puede salirse del atributo. El esquema y la ruta son
		constantes; lo único variable es el token, y va escapado.
	*/
	appURL := template.URL("zports://invite/" + url.PathEscape(token))

	// El detalle del partido no se repite acá: quien tiene la app la abre y lo
	// ve completo, y quien no, lo va a ver apenas la instale. Esta página no es
	// la invitación, es el puente hacia ella.
	h.renderPage(c, http.StatusOK, invitePageData{
		Title:     "Te invitaron a jugar",
		Body:      "Descarga ZPORTS para ver el partido y confirmar si vas.",
		AppURL:    appURL,
		StoreURL:  h.cfg.PlayStoreURL,
		ShowLinks: true,
	})
}

type invitePageData struct {
	Title string
	Body  string
	// AppURL abre la app si está instalada. Es el respaldo de App Links: cuando
	// la verificación no está configurada, o falló, el sistema no intercepta y
	// este botón sigue funcionando para quien sí tiene la app.
	//
	// Es template.URL porque `zports://` no es un esquema que html/template deje
	// pasar solo. Ver dónde se construye: el token va escapado.
	AppURL    template.URL
	StoreURL  string
	ShowLinks bool
}

func (h *AppLinksHandler) renderPage(c *gin.Context, status int, data invitePageData) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	/*
		La CSP de la API no sirve para esta página.

		`SecureHeaders` manda `default-src 'none'`, que es lo correcto para
		respuestas JSON pero bloquea el <style> de acá: el navegador descarta la
		hoja entera y muestra la invitación como HTML pelado, con serif y links
		azules. No se ve con curl —que ignora la CSP— así que el síntoma solo
		aparece en un teléfono de verdad, que es donde esta página vive.

		Se reemplaza por la mínima que la deja andar. Sigue sin poder ejecutar
		JavaScript ni traer nada de afuera: `default-src 'none'` cubre scripts,
		imágenes y conexiones, y lo único que se habilita son estilos inline.
		`'unsafe-inline'` acá no abre nada: la página no tiene scripts y lo único
		variable que se escribe es el token, que va escapado dentro de un href y
		nunca cerca de un bloque de estilos.
	*/
	c.Header(
		"Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	)
	// Una invitación cambia de estado cuando se llena el cupo, así que la página
	// no se puede cachear: mostraría lugares que ya no existen.
	c.Header("Cache-Control", "no-store")
	_ = invitePageTemplate.Execute(c.Writer, data)
}

// invitePageTemplate usa html/template y no concatenación: el token viene de la
// URL y termina adentro de un href. Con text/template o un Sprintf, ese valor se
// escribiría crudo en el HTML.
var invitePageTemplate = template.Must(template.New("invite").Parse(strings.TrimSpace(`
<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ZPORTS</title>
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: #0F4C29; color: #fff; padding: 24px; box-sizing: border-box;
  }
  main { max-width: 420px; width: 100%; text-align: center; }
  h1 { font-size: 26px; line-height: 1.25; margin: 0 0 12px; }
  p { font-size: 16px; line-height: 1.5; opacity: .85; margin: 0 0 28px; }
  a {
    display: block; padding: 15px 20px; border-radius: 12px; text-decoration: none;
    font-weight: 600; font-size: 16px; margin-bottom: 12px;
  }
  .primary { background: #fff; color: #0F4C29; }
  .secondary { border: 1px solid rgba(255,255,255,.4); color: #fff; }
</style>
</head>
<body>
<main>
  <h1>{{ .Title }}</h1>
  <p>{{ .Body }}</p>
  {{ if .ShowLinks }}
    {{ if .StoreURL }}<a class="primary" href="{{ .StoreURL }}">Descargar ZPORTS</a>{{ end }}
    {{ if .AppURL }}<a class="secondary" href="{{ .AppURL }}">Ya tengo la app</a>{{ end }}
  {{ end }}
</main>
</body>
</html>
`)))
