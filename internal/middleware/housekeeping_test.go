package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
)

// Lo que hay que no romper es la cota: muchas requests seguidas son una sola
// corrida. Si se dispara en cada una, la barrida vuelve a ser un UPDATE por
// request, que es justo lo que se sacó del camino de lectura.
func TestHousekeeping_RunsOncePerInterval(t *testing.T) {
	var runs atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)

	r := gin.New()
	r.Use(middleware.Housekeeping(middleware.Task{
		Every: time.Hour,
		Run: func(context.Context) {
			runs.Add(1)
			wg.Done()
		},
	}))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	var requests sync.WaitGroup
	for range 20 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
			assert.Equal(t, http.StatusOK, w.Code)
		}()
	}
	requests.Wait()
	wg.Wait()

	assert.Equal(t, int32(1), runs.Load())
}

// Pasado el intervalo vuelve a correr: si no, una máquina que vive semanas
// barrería una sola vez.
func TestHousekeeping_RunsAgainAfterInterval(t *testing.T) {
	done := make(chan struct{}, 4)
	r := gin.New()
	r.Use(middleware.Housekeeping(middleware.Task{
		Every: 10 * time.Millisecond,
		Run:   func(context.Context) { done <- struct{}{} },
	}))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	for range 2 {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("la tarea no corrió")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
