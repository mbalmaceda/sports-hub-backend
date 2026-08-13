package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const claimsKey = "claims"

const bearerPrefix = "bearer "

func Middleware(signer *Signer) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := signer.ParseAccessToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

// bearerToken saca el token del header Authorization. El esquema se compara sin
// distinguir mayúsculas porque el RFC 7235 lo define así y hay clientes HTTP que
// mandan "bearer"; antes solo se aceptaba "Bearer " exacto.
func bearerToken(header string) (string, bool) {
	if len(header) < len(bearerPrefix) {
		return "", false
	}
	if !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

func ClaimsFromContext(c *gin.Context) (*Claims, bool) {
	val, exists := c.Get(claimsKey)
	if !exists {
		return nil, false
	}
	claims, ok := val.(*Claims)
	return claims, ok
}
