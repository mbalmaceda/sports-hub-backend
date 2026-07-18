package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const claimsKey = "claims"

func Middleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := ParseAccessToken(strings.TrimPrefix(header, "Bearer "), secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

func ClaimsFromContext(c *gin.Context) (*Claims, bool) {
	val, exists := c.Get(claimsKey)
	if !exists {
		return nil, false
	}
	claims, ok := val.(*Claims)
	return claims, ok
}
