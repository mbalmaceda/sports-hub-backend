package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var defaultCORSAllowedOrigins = []string{
	"http://localhost:8081",
	"http://localhost:8082",
	"http://localhost:19006",
	"http://localhost:19000",
}

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
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

	return Config{
		Port:                   port,
		DatabaseURL:            dbURL,
		JWTSecret:              jwtSecret,
		FirebaseServiceAccount: os.Getenv("FIREBASE_SERVICE_ACCOUNT"),
		CORSEnabled:            corsEnabled,
		CORSAllowedOrigins:     corsAllowedOrigins,
	}, nil
}
