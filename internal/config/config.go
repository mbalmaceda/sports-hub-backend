package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var defaultCORSAllowedOrigins = []string{
	"http://localhost:8081",
	"http://localhost:8082",
	"http://localhost:19006",
	"http://localhost:19000",
}

// minJWTSecretLen son los 32 bytes que pide HS256 para que la clave no sea el
// eslabón débil de la firma. El .env.example trae un placeholder y sin esta
// validación nada impide desplegar con él.
const minJWTSecretLen = 32

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	// JWTSecretPrevious permite rotar JWT_SECRET sin desloguear a nadie: se
	// firma siempre con el actual y se acepta también el anterior mientras
	// duren los access tokens ya emitidos. Vencida esa ventana se borra.
	JWTSecretPrevious string
	// FirebaseServiceAccount es el JSON de la cuenta de servicio, ya resuelto:
	// quien lo lee siempre recibe el contenido, nunca una ruta.
	//
	// La variable de entorno acepta las dos formas —ver `resolveServiceAccount`—
	// porque los dos entornos piden cosas distintas: en Fly los secretos son
	// variables y el JSON va inline; en local, pegar un JSON multilínea en un
	// `.env` rompe el archivo entero, así que ahí va la ruta al archivo.
	//
	// Es opcional: sin ella el backend arranca igual y lo único que no funciona
	// es lo que necesita Firebase. Exigirla dejaría la API caída por una función
	// que todavía no está en uso.
	FirebaseServiceAccount string
	CORSEnabled            bool
	CORSAllowedOrigins     []string
	// CORSAllowedOriginRegex cubre los preview deploys de Vercel, que estrenan
	// subdominio en cada commit y por eso no se pueden enumerar. Va acotado al
	// proyecto: un `.vercel.app` a secas habilitaría el deploy de cualquiera.
	CORSAllowedOriginRegex *regexp.Regexp
	// MaxBodyBytes corta el cuerpo de cada request. Ningún endpoint recibe
	// archivos —los comprobantes viajan como URL—, así que 1 MB sobra.
	MaxBodyBytes int64

	// ── Enlaces de invitación ────────────────────────────────────────────────
	//
	// El enlace que se comparte por WhatsApp apunta acá, no a la app: quien lo
	// recibe es por definición alguien que todavía no la tiene instalada. Este
	// servidor sirve una página que muestra la invitación y, si la app está,
	// deja que el sistema la abra directo (App Links / Universal Links).

	// PublicBaseURL es el origen con el que se arman los enlaces compartibles,
	// sin barra final. Vacío desactiva la página de invitación: mejor no servir
	// nada que servir enlaces que apuntan a un host equivocado. Tiene que ser
	// https salvo que apunte a la red local — ver `validatePublicBaseURL`.
	PublicBaseURL string
	// AndroidPackageName y AndroidCertFingerprints alimentan assetlinks.json,
	// que es lo que hace que Android abra la app en vez del navegador. Los
	// fingerprints son SHA-256 en mayúsculas separados por dos puntos, tal como
	// los muestra Play Console en "Firma de apps". Puede haber más de uno: la
	// clave de subida y la de firma de Play son distintas.
	AndroidPackageName      string
	AndroidCertFingerprints []string
	// AppleAppID es "<TeamID>.<BundleID>". Sin cuenta de Apple Developer todavía
	// no existe, y sin esto no se sirve el apple-app-site-association.
	AppleAppID string
	// PlayStoreURL es a dónde mandar a quien no tiene la app. Vacío esconde el
	// botón en vez de llevar a una página rota.
	PlayStoreURL string

	// ── Google Sign-In ───────────────────────────────────────────────────────

	// GoogleClientIDs son las audiencias que se aceptan en el ID token: el
	// client id de Android, el de iOS y el web, separados por coma. Van todas
	// porque cada plataforma emite el token con la suya, y el chequeo de
	// audiencia es lo que impide que un token emitido para otra app de Google
	// sirva para entrar acá. Vacío desactiva el endpoint.
	GoogleClientIDs []string
}

// GoogleSignInEnabled indica si se puede entrar con Google.
func (c Config) GoogleSignInEnabled() bool {
	return len(c.GoogleClientIDs) > 0
}

// InviteLinksEnabled indica si se puede armar y servir un enlace compartible.
func (c Config) InviteLinksEnabled() bool {
	return c.PublicBaseURL != ""
}

func Load() (Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if len(jwtSecret) < minJWTSecretLen {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least %d bytes", minJWTSecretLen)
	}
	// El anterior es opcional, pero si está tiene que ser una clave de verdad y
	// no la misma: repetirla haría creer que hay rotación cuando no la hay.
	jwtSecretPrevious := os.Getenv("JWT_SECRET_PREVIOUS")
	if jwtSecretPrevious != "" {
		if len(jwtSecretPrevious) < minJWTSecretLen {
			return Config{}, fmt.Errorf("JWT_SECRET_PREVIOUS must be at least %d bytes", minJWTSecretLen)
		}
		if jwtSecretPrevious == jwtSecret {
			return Config{}, fmt.Errorf("JWT_SECRET_PREVIOUS must differ from JWT_SECRET")
		}
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	corsEnabled := true
	if raw := os.Getenv("CORS_ENABLED"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CORS_ENABLED must be a boolean: %w", err)
		}
		corsEnabled = parsed
	}

	corsAllowedOrigins := defaultCORSAllowedOrigins
	if raw := os.Getenv("CORS_ALLOWED_ORIGINS"); raw != "" {
		origins := make([]string, 0)
		for _, origin := range strings.Split(raw, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				origins = append(origins, origin)
			}
		}
		corsAllowedOrigins = origins
	}

	var corsRegex *regexp.Regexp
	if raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGIN_REGEX")); raw != "" {
		compiled, err := regexp.Compile(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CORS_ALLOWED_ORIGIN_REGEX is not a valid regexp: %w", err)
		}
		// Sin anclas el patrón matchea en cualquier parte del origen, así que
		// `sports-hub.vercel.app` dejaría entrar a `evil.com/sports-hub.vercel.app`.
		if !strings.HasPrefix(raw, "^") || !strings.HasSuffix(raw, "$") {
			return Config{}, fmt.Errorf("CORS_ALLOWED_ORIGIN_REGEX must be anchored with ^ and $")
		}
		corsRegex = compiled
	}

	maxBodyBytes := int64(1 << 20)
	if raw := os.Getenv("MAX_BODY_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("MAX_BODY_BYTES must be a positive integer")
		}
		maxBodyBytes = parsed
	}

	// La barra final se recorta acá y no en cada uso: pegada a una ruta produce
	// `//i/token`, que en algunos proxies no es lo mismo que `/i/token`.
	publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	if publicBaseURL != "" {
		if err := validatePublicBaseURL(publicBaseURL); err != nil {
			return Config{}, err
		}
	}

	fingerprints := make([]string, 0)
	for _, raw := range strings.Split(os.Getenv("ANDROID_CERT_FINGERPRINTS"), ",") {
		if trimmed := strings.ToUpper(strings.TrimSpace(raw)); trimmed != "" {
			fingerprints = append(fingerprints, trimmed)
		}
	}

	googleClientIDs := make([]string, 0)
	for _, raw := range strings.Split(os.Getenv("GOOGLE_CLIENT_IDS"), ",") {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			googleClientIDs = append(googleClientIDs, trimmed)
		}
	}

	androidPackage := strings.TrimSpace(os.Getenv("ANDROID_PACKAGE_NAME"))
	if androidPackage == "" {
		androidPackage = "com.zports.app"
	}

	serviceAccount, err := resolveServiceAccount(os.Getenv("FIREBASE_SERVICE_ACCOUNT"))
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:                    port,
		DatabaseURL:             dbURL,
		JWTSecret:               jwtSecret,
		JWTSecretPrevious:       jwtSecretPrevious,
		FirebaseServiceAccount:  serviceAccount,
		CORSEnabled:             corsEnabled,
		CORSAllowedOrigins:      corsAllowedOrigins,
		CORSAllowedOriginRegex:  corsRegex,
		MaxBodyBytes:            maxBodyBytes,
		PublicBaseURL:           publicBaseURL,
		AndroidPackageName:      androidPackage,
		AndroidCertFingerprints: fingerprints,
		AppleAppID:              strings.TrimSpace(os.Getenv("APPLE_APP_ID")),
		PlayStoreURL:            strings.TrimSpace(os.Getenv("PLAY_STORE_URL")),
		GoogleClientIDs:         googleClientIDs,
	}, nil
}

/*
validatePublicBaseURL exige https, salvo que el enlace apunte a la propia red.

Android e iOS solo verifican App Links sobre HTTPS: un http en producción
abriría el navegador en vez de la app, en silencio, y por eso el arranque falla
antes que servir enlaces así.

En desarrollo no hay TLS. El backend corre en la LAN y el teléfono abre la
página por IP, así que exigir https dejaba dos salidas y las dos malas: levantar
un túnel para probar el flujo de parches, o dejar la variable vacía y que la app
avise que las invitaciones no están habilitadas. Probar el enlace era más caro
que escribirlo.

El corte es el destino y no un flag de entorno —no hay ninguno en esta config, y
un `APP_ENV=dev` sería justo la clase de perilla que alguien deja prendida en el
lugar equivocado—. Loopback y los rangos privados de la RFC 1918 no son
alcanzables desde afuera: un http ahí no puede ser producción mal configurada,
porque un enlace así no le llega a nadie. Cualquier host público sigue exigiendo
https.
*/
func validatePublicBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("PUBLIC_BASE_URL is not a valid URL: %w", err)
	}

	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLocalHost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf(
			"PUBLIC_BASE_URL must start with https:// (http is only allowed for localhost or a private network address)",
		)
	default:
		return fmt.Errorf("PUBLIC_BASE_URL must start with https://")
	}
}

// isLocalHost reconoce las direcciones que no salen de la máquina o de la red
// local: el `localhost` del emulador y la IP con la que el teléfono llega al
// backend por wifi.
func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

/*
resolveServiceAccount acepta la credencial de Firebase de las dos formas en que
se la puede tener a mano, y devuelve siempre el JSON.

  - Empieza con "{"  → es el JSON inline. Es lo que hay en Fly, donde los
    secretos son variables de entorno y no hay dónde poner un archivo.
  - Cualquier otra cosa → es la ruta al archivo que baja la consola de Firebase.
    Es lo que corresponde en local: ese JSON tiene saltos de línea, y pegarlo
    dentro de un `.env` no rompe solo esa línea, rompe el archivo entero —
    godotenv corta al primer renglón que no puede leer y descarta TODAS las
    variables, así que el síntoma termina siendo "DATABASE_URL is required" y
    nadie mira a Firebase.

Una ruta que no se puede leer es un error y no un "Firebase apagado". Seguir en
silencio es exactamente lo que hace que un secreto mal configurado se descubra
tres pantallas después, cuando algo no anda y nada dice por qué.
*/
func resolveServiceAccount(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "{") {
		return value, nil
	}

	// El `~` lo expande el shell, y acá el valor puede venir de un `.env`, que
	// no pasa por ningún shell.
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("FIREBASE_SERVICE_ACCOUNT: no se pudo resolver %q: %w", value, err)
		}
		value = filepath.Join(home, value[2:])
	}

	data, err := os.ReadFile(value)
	if err != nil {
		return "", fmt.Errorf(
			"FIREBASE_SERVICE_ACCOUNT parece una ruta pero no se pudo leer (%s). "+
				"Tiene que ser la ruta al JSON de la cuenta de servicio, o el JSON inline empezando con '{'", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return "", fmt.Errorf("FIREBASE_SERVICE_ACCOUNT: %s no parece un JSON de cuenta de servicio", value)
	}
	return string(data), nil
}

// AllowOrigin decide si un Origin del navegador entra. Se pasa como
// AllowOriginFunc y solo lo consulta el middleware cuando el origen no estaba
// en la lista exacta, así que acá únicamente se resuelven los previews.
func (c Config) AllowOrigin(origin string) bool {
	return c.CORSAllowedOriginRegex != nil && c.CORSAllowedOriginRegex.MatchString(origin)
}

// Timeouts del servidor HTTP. No son configurables a propósito: son propiedades
// del borde, no de un despliegue, y tenerlos en variables invita a apagarlos.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 15 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 60 * time.Second
	ShutdownTimeout   = 10 * time.Second
)
