package handlers

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// requireInternalToken guards server-to-server finals endpoints (pipeline, runner).
// With no token configured the routes don't exist as far as a caller can tell.
func requireInternalToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.Next()
	}
}

// RequireInternalToken is the exported form for route setup.
func RequireInternalToken(token string) gin.HandlerFunc { return requireInternalToken(token) }
