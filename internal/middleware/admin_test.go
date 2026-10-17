package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func adminRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/ping", AdminToken(token), func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestAdminToken(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		header string
		want   int
	}{
		{"sin secreto el grupo no existe", "", "lo-que-sea", http.StatusNotFound},
		{"sin header", "llave-de-servicio", "", http.StatusUnauthorized},
		{"llave equivocada", "llave-de-servicio", "otra", http.StatusUnauthorized},
		{"llave correcta", "llave-de-servicio", "llave-de-servicio", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/ping", nil)
			if tc.header != "" {
				req.Header.Set(AdminTokenHeader, tc.header)
			}
			w := httptest.NewRecorder()
			adminRouter(tc.secret).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, se esperaba %d", w.Code, tc.want)
			}
		})
	}
}
