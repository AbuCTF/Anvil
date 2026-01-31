package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Logger middleware for request logging
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("ip", c.ClientIP()),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.String("request-id", c.GetString("request_id")),
		)
	}
}

// RequestID middleware adds a unique request ID
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// CORS middleware
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// SecurityHeaders middleware adds security-related headers
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Next()
	}
}

// Claims represents JWT claims
type Claims struct {
	UserID    uuid.UUID `json:"user_id,omitempty"`
	SessionID uuid.UUID `json:"session_id,omitempty"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	TokenType string    `json:"token_type"` // "user" or "team"
	jwt.RegisteredClaims
}

// Auth middleware validates JWT tokens
func Auth(cfg *config.Config, db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Authorization header required",
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid authorization header format",
			})
			return
		}

		tokenString := parts[1]

		// Parse and validate token
		token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(cfg.JWT.Secret), nil
		})

		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or expired token",
			})
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid token claims",
			})
			return
		}

		// Check if it's a user token or team token
		if claims.TokenType == "user" {
			// Verify user still exists and is active
			var status string
			err := db.Pool.QueryRow(c.Request.Context(),
				"SELECT status FROM users WHERE id = $1",
				claims.UserID,
			).Scan(&status)

			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "User not found",
				})
				return
			}

			if status != "active" {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "Account is " + status,
				})
				return
			}

			c.Set("user_id", claims.UserID)
			c.Set("username", claims.Username)
			c.Set("role", claims.Role)
			c.Set("token_type", "user")
		} else if claims.TokenType == "team" {
			// For team tokens, verify session is still valid
			var expiresAt time.Time
			err := db.Pool.QueryRow(c.Request.Context(),
				"SELECT expires_at FROM sessions WHERE id = $1",
				claims.SessionID,
			).Scan(&expiresAt)

			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "Session not found",
				})
				return
			}

			if time.Now().After(expiresAt) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "Session expired",
				})
				return
			}

			c.Set("session_id", claims.SessionID)
			c.Set("username", claims.Username)
			c.Set("role", "user") // Team tokens are always user role
			c.Set("token_type", "team")
		}

		c.Next()
	}
}

// RequireRole middleware checks if user has required role
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Role not found in context",
			})
			return
		}

		role := userRole.(string)
		for _, r := range roles {
			if role == r {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Insufficient permissions",
		})
	}
}

// GetUserID extracts user ID from context (handles both user and session)
func GetUserID(c *gin.Context) *uuid.UUID {
	if userID, exists := c.Get("user_id"); exists {
		id := userID.(uuid.UUID)
		return &id
	}
	return nil
}

// GetSessionID extracts session ID from context
func GetSessionID(c *gin.Context) *uuid.UUID {
	if sessionID, exists := c.Get("session_id"); exists {
		id := sessionID.(uuid.UUID)
		return &id
	}
	return nil
}

// GetIdentifier returns either user_id or session_id based on token type
func GetIdentifier(c *gin.Context) (userID *uuid.UUID, sessionID *uuid.UUID) {
	tokenType, _ := c.Get("token_type")
	if tokenType == "user" {
		return GetUserID(c), nil
	}
	return nil, GetSessionID(c)
}
