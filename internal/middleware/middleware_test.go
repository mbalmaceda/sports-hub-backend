package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Este es el test que cubre la alerta original de Gin. No comprueba que la
// alerta desaparezca —eso pasaría igual con solo dejar de usar r.Run()— sino lo
// único que importa: que un X-Forwarded-For inventado ya no defina quién es el
// cliente, porque de eso depende que los limitadores por IP sirvan de algo.
func TestConfigureTrustedProxies_IgnoresForgedForwardedFor(t *testing.T) {
	r := gin.New()
	require.NoError(t, middleware.ConfigureTrustedProxies(r))

	var seen string
	r.GET("/x", func(c *gin.Context) {
		seen = c.ClientIP()
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "10.0.0.1", seen, "el header del cliente no puede ganarle al socket real")
}

// Detrás de Fly, la IP verdadera llega en Fly-Client-IP, que el edge reescribe.
func TestConfigureTrustedProxies_UsesFlyClientIP(t *testing.T) {
	r := gin.New()
	require.NoError(t, middleware.ConfigureTrustedProxies(r))

	var seen string
	r.GET("/x", func(c *gin.Context) {
		seen = c.ClientIP()
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("Fly-Client-IP", "200.1.2.3")
	r.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "200.1.2.3", seen)
}

func TestRateLimit_AllowsBurstThenRejects(t *testing.T) {
	limiter := middleware.New(3, time.Minute)
	defer limiter.Stop()

	for i := range 3 {
		ok, _ := limiter.Allow("1.2.3.4")
		assert.True(t, ok, "la petición %d entra dentro del burst", i+1)
	}

	ok, retryAfter := limiter.Allow("1.2.3.4")
	assert.False(t, ok, "la cuarta se pasa del límite")
	assert.Positive(t, retryAfter, "hay que decirle al cliente cuándo volver")
}

// Cada clave tiene su propio cupo: bloquear a un atacante no puede bloquear al
// resto de los usuarios.
func TestRateLimit_KeysAreIndependent(t *testing.T) {
	limiter := middleware.New(1, time.Minute)
	defer limiter.Stop()

	ok, _ := limiter.Allow("1.2.3.4")
	require.True(t, ok)
	ok, _ = limiter.Allow("1.2.3.4")
	require.False(t, ok)

	ok, _ = limiter.Allow("5.6.7.8")
	assert.True(t, ok, "otra IP no debería verse afectada")
}

// Un rechazo no puede consumir cupo futuro: si lo hiciera, un cliente bloqueado
// que reintenta se empujaría su propia espera hacia adelante para siempre.
func TestRateLimit_RejectionDoesNotConsumeQuota(t *testing.T) {
	limiter := middleware.New(1, 2*time.Second)
	defer limiter.Stop()

	ok, _ := limiter.Allow("1.2.3.4")
	require.True(t, ok)

	_, firstRetry := limiter.Allow("1.2.3.4")
	_, secondRetry := limiter.Allow("1.2.3.4")

	assert.LessOrEqual(t, secondRetry, firstRetry,
		"reintentar no debería alejar el momento en que vuelve a haber cupo")
}

func TestRateLimit_MiddlewareReturns429WithRetryAfter(t *testing.T) {
	limiter := middleware.New(1, time.Minute)
	defer limiter.Stop()

	r := gin.New()
	r.GET("/x", limiter.Handle(func(*gin.Context) string { return "same-key" }),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	first := httptest.NewRecorder()
	r.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, http.StatusTooManyRequests, second.Code)

	retryAfter, err := strconv.Atoi(second.Header().Get("Retry-After"))
	require.NoError(t, err, "Retry-After tiene que venir y ser un entero de segundos")
	assert.Positive(t, retryAfter)
}

// Sin clave no se puede identificar a nadie, y rechazar a todos sería peor que
// dejar pasar lo que no supimos clasificar.
func TestRateLimit_EmptyKeyPasses(t *testing.T) {
	limiter := middleware.New(1, time.Minute)
	defer limiter.Stop()

	r := gin.New()
	r.GET("/x", limiter.Handle(func(*gin.Context) string { return "" }),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	for range 5 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		assert.Equal(t, http.StatusOK, w.Code)
	}
}

func TestBodyLimit_RejectsOversizedBody(t *testing.T) {
	r := gin.New()
	r.Use(middleware.BodyLimit(64))
	r.POST("/x", func(c *gin.Context) {
		if _, err := c.GetRawData(); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 500)))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// Sin Content-Length declarado el corte lo tiene que hacer igual el reader.
func TestBodyLimit_RejectsOversizedChunkedBody(t *testing.T) {
	r := gin.New()
	r.Use(middleware.BodyLimit(64))
	r.POST("/x", func(c *gin.Context) {
		if _, err := c.GetRawData(); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 500)))
	req.ContentLength = -1

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestBodyLimit_AllowsSmallBody(t *testing.T) {
	r := gin.New()
	r.Use(middleware.BodyLimit(1024))
	r.POST("/x", func(c *gin.Context) {
		_, err := c.GetRawData()
		require.NoError(t, err)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"ok":true}`)))

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecureHeaders(t *testing.T) {
	r := gin.New()
	r.Use(middleware.SecureHeaders())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
	assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=31536000")
	// .fly.dev es un dominio compartido: inscribirlo en la lista de precarga de
	// los navegadores afectaría a apps que no son nuestras.
	assert.NotContains(t, w.Header().Get("Strict-Transport-Security"), "preload")
}

func TestRequestID_IsSetAndReturned(t *testing.T) {
	r := gin.New()
	r.Use(middleware.RequestID())

	var seen string
	r.GET("/x", func(c *gin.Context) {
		seen = middleware.RequestIDFromContext(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.NotEmpty(t, seen)
	assert.Equal(t, seen, w.Header().Get("X-Request-Id"))
}

// El id que manda el cliente no se reutiliza: podría repetirlo a propósito para
// mezclar el rastro de dos requests distintas.
func TestRequestID_IgnoresClientSupplied(t *testing.T) {
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "forjado-por-el-cliente")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, "forjado-por-el-cliente", w.Header().Get("X-Request-Id"))
}

// Un 400 que los handlers devuelven solo en el body no puede quedar en un log
// que diga únicamente "status=400": sin el mensaje no hay forma de saber qué
// falló. El log de errores tiene que llevar el cuerpo de la respuesta.
func TestLogger_IncludesErrorResponseBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_uses is too high"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Contains(t, buf.String(), "status=400")
	assert.Contains(t, buf.String(), "max_uses is too high")
}

// Lo que devuelve una respuesta exitosa no se loguea: los 200/201 pueden
// llevar datos personales o tokens y no son un error.
func TestLogger_SuccessDoesNotLogResponseBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"token": "no-debe-salir-en-el-log"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.NotContains(t, buf.String(), "no-debe-salir-en-el-log")
}

// El cuerpo capturado se recorta para que un error gigante no inunde el log.
func TestLogger_TruncatesLargeResponseBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(middleware.Logger(logger))
	r.GET("/x", func(c *gin.Context) {
		c.String(http.StatusBadRequest, strings.Repeat("a", 4096))
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Contains(t, buf.String(), "...")
	assert.Contains(t, buf.String(), strings.Repeat("a", 512))
}
