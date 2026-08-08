package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
)

type FirebaseHandler struct {
	firebase *firebase.Firebase
}

func NewFirebaseHandler(fb *firebase.Firebase) *FirebaseHandler {
	return &FirebaseHandler{firebase: fb}
}

// IssueToken POST /auth/firebase-token
//
// Cambia la sesión de ZPORTS por una de Firebase para el mismo usuario. La ruta
// está protegida por el middleware de siempre, así que llegar hasta acá ya
// significa tener un JWT válido: el uid sale de ese token y nunca del cuerpo del
// request, porque si no cualquiera pediría un token a nombre de otro.
func (h *FirebaseHandler) IssueToken(c *gin.Context) {
	claims, _ := auth.ClaimsFromContext(c)

	token, err := h.firebase.CustomToken(c.Request.Context(), claims.UserID)
	if errors.Is(err, firebase.ErrNotConfigured) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "firebase is not configured"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue firebase token"})
		return
	}

	// Dura una hora y lo emite este backend, así que no se guarda en ningún
	// lado: cuando vence se pide otro.
	c.JSON(http.StatusOK, gin.H{"token": token})
}
