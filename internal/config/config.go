package config

import (
	"fmt"
	"os"
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
	// FirebaseServiceAccount es el JSON de la cuenta de servicio, entero, tal
	// como lo entrega la consola de Firebase. Va como variable y no como archivo
	// porque en Fly los secretos son variables de entorno.
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

	return Config{
		Port:                   port,
		DatabaseURL:            dbURL,
		JWTSecret:              jwtSecret,
		JWTSecretPrevious:      jwtSecretPrevious,
		FirebaseServiceAccount: os.Getenv("FIREBASE_SERVICE_ACCOUNT"),
		CORSEnabled:            corsEnabled,
		CORSAllowedOrigins:     corsAllowedOrigins,
		CORSAllowedOriginRegex: corsRegex,
		MaxBodyBytes:           maxBodyBytes,
	}, nil
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
