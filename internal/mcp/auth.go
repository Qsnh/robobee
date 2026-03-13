package mcp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIKeyMiddleware returns a Gin middleware that requires X-API-Key header to match key.
func APIKeyMiddleware(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-API-Key") != key {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
