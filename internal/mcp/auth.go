package mcp

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIKeyMiddleware returns a Gin middleware that requires X-API-Key header or api_key query param to match key.
func APIKeyMiddleware(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		candidate := c.GetHeader("X-API-Key")
		if candidate == "" {
			candidate = c.Query("api_key")
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
