package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
)

func loggedRouter(buf *bytes.Buffer, healthStatus int) *gin.Engine {
	r := gin.New()
	r.Use(middleware.Logger(slog.New(slog.NewJSONHandler(buf, nil))))
	r.GET("/health", func(c *gin.Context) { c.Status(healthStatus) })
	r.GET("/teams", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

// El chequeo de Fly llega cada 30 segundos: si sale bien no se loguea, porque
// tapaba todo lo demás en Grafana.
func TestLogger_SkipsHealthyHealthCheck(t *testing.T) {
	var buf bytes.Buffer
	r := loggedRouter(&buf, http.StatusOK)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Empty(t, buf.String())
}

// Si el chequeo falla sí se loguea: es justo lo que hay que ver.
func TestLogger_LogsFailingHealthCheck(t *testing.T) {
	var buf bytes.Buffer
	r := loggedRouter(&buf, http.StatusServiceUnavailable)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Contains(t, buf.String(), `"route":"/health"`)
}

func TestLogger_LogsOtherRoutes(t *testing.T) {
	var buf bytes.Buffer
	r := loggedRouter(&buf, http.StatusOK)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/teams", nil))

	assert.Contains(t, buf.String(), `"route":"/teams"`)
}
