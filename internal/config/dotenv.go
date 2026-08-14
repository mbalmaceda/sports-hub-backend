package config

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
)

/*
LoadDotEnv carga el `.env` si existe, y **avisa cuando existe pero está roto**.

Esa es toda la diferencia con llamar a `godotenv.Load()` y descartar el error,
que es lo que se hacía antes. godotenv no salta la línea que no entiende: corta
el archivo entero y no devuelve ninguna variable. Descartando el error, un `.env`
con un solo renglón mal escrito se comporta igual que no tener `.env`, y el
programa falla más adelante quejándose de la primera variable que le falta —
"DATABASE_URL is required"— sin ninguna pista de que el problema es de sintaxis
y está veinte líneas más abajo.

No tener `.env` sigue siendo normal y no dice nada: en producción las variables
vienen del entorno y el archivo no existe.
*/
func LoadDotEnv() {
	err := godotenv.Load()
	if err == nil {
		return
	}
	// Que no exista es el caso de producción, no un problema.
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	// Existe pero no se pudo leer entero. Se avisa fuerte porque el síntoma
	// aparece lejos de la causa.
	if _, statErr := os.Stat(".env"); statErr == nil {
		slog.Error("el archivo .env existe pero no se pudo parsear: "+
			"ninguna variable del archivo fue cargada. Cada línea tiene que ser CLAVE=valor "+
			"(sin comandos de shell, sin barras de continuación, sin JSON multilínea)",
			"error", err)
		return
	}
	slog.Warn("no se pudo cargar .env", "error", err)
}
