package middleware

import (
	"errors"
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

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, If-None-Match, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, ETag, X-Anvil-Cache")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

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

type Claims struct {
	UserID    uuid.UUID `json:"user_id,omitempty"`
	SessionID uuid.UUID `json:"session_id,omitempty"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	TokenType string    `json:"token_type"` // "user" or "team"
	jwt.RegisteredClaims
}

func parseAccessToken(tokenString string, cfg *config.Config) (*Claims, error) {
	parserOptions := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	}
	if cfg.JWT.Issuer != "" {
		parserOptions = append(parserOptions, jwt.WithIssuer(cfg.JWT.Issuer))
	}

	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(cfg.JWT.Secret), nil
		},
		parserOptions...,
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}

	switch claims.TokenType {
	case "user":
		if claims.UserID == uuid.Nil {
			return nil, errors.New("user token missing user ID")
		}
	case "team":
		if claims.SessionID == uuid.Nil {
			return nil, errors.New("team token missing session ID")
		}
	default:
		return nil, errors.New("invalid token type")
	}

	return claims, nil
}

func Auth(cfg *config.Config, db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			APIError(c, http.StatusUnauthorized, "Authorization header required")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			APIError(c, http.StatusUnauthorized, "Invalid authorization header format")
			return
		}

		tokenString := parts[1]

		claims, err := parseAccessToken(tokenString, cfg)
		if err != nil {
			APIError(c, http.StatusUnauthorized, "Invalid or expired token")
			return
		}

		if claims.TokenType == "user" {
			// role and username come from the database so account changes take
			// effect without waiting for token expiry.
			var username, role, status string
			err := db.Pool.QueryRow(c.Request.Context(),
				"SELECT username, role, status FROM users WHERE id = $1",
				claims.UserID,
			).Scan(&username, &role, &status)

			if err != nil {
				APIError(c, http.StatusUnauthorized, "User not found")
				return
			}

			if status != "active" {
				APIError(c, http.StatusForbidden, "Account is "+status)
				return
			}

			c.Set("user_id", claims.UserID)
			c.Set("username", username)
			c.Set("role", role)
			c.Set("token_type", "user")
		} else if claims.TokenType == "team" {
			var expiresAt time.Time
			var teamName string
			err := db.Pool.QueryRow(c.Request.Context(),
				`SELECT s.expires_at, t.team_name
				 FROM sessions s
				 JOIN team_tokens t ON t.id = s.token_id
				 WHERE s.id = $1`,
				claims.SessionID,
			).Scan(&expiresAt, &teamName)

			if err != nil {
				APIError(c, http.StatusUnauthorized, "Session not found")
				return
			}

			if time.Now().After(expiresAt) {
				APIError(c, http.StatusUnauthorized, "Session expired")
				return
			}

			c.Set("session_id", claims.SessionID)
			c.Set("username", teamName)
			c.Set("role", "user") // team tokens are always user role
			c.Set("token_type", "team")
		}

		c.Next()
	}
}

// like Auth, but never aborts: a valid token populates the context, anything
// else falls through unauthenticated.
func OptionalAuth(cfg *config.Config, db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.Next()
			return
		}

		tokenString := parts[1]

		claims, err := parseAccessToken(tokenString, cfg)
		if err != nil {
			c.Next()
			return
		}

		if claims.TokenType == "user" {
			var username, role, status string
			err := db.Pool.QueryRow(c.Request.Context(),
				"SELECT username, role, status FROM users WHERE id = $1",
				claims.UserID,
			).Scan(&username, &role, &status)

			if err != nil || status != "active" {
				c.Next()
				return
			}

			c.Set("user_id", claims.UserID)
			c.Set("username", username)
			c.Set("role", role)
			c.Set("token_type", "user")
		} else if claims.TokenType == "team" {
			var expiresAt time.Time
			var teamName string
			err := db.Pool.QueryRow(c.Request.Context(),
				`SELECT s.expires_at, t.team_name
				 FROM sessions s
				 JOIN team_tokens t ON t.id = s.token_id
				 WHERE s.id = $1`,
				claims.SessionID,
			).Scan(&expiresAt, &teamName)

			if err != nil || time.Now().After(expiresAt) {
				c.Next()
				return
			}

			c.Set("session_id", claims.SessionID)
			c.Set("username", teamName)
			c.Set("role", "user")
			c.Set("token_type", "team")
		}

		c.Next()
	}
}

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

func GetUserID(c *gin.Context) *uuid.UUID {
	if userID, exists := c.Get("user_id"); exists {
		if id, ok := userID.(uuid.UUID); ok {
			return &id
		}
	}
	return nil
}

func GetSessionID(c *gin.Context) *uuid.UUID {
	if sessionID, exists := c.Get("session_id"); exists {
		if id, ok := sessionID.(uuid.UUID); ok {
			return &id
		}
	}
	return nil
}

func GetIdentifier(c *gin.Context) (userID *uuid.UUID, sessionID *uuid.UUID) {
	tokenType, _ := c.Get("token_type")
	if tokenType == "user" {
		return GetUserID(c), nil
	}
	return nil, GetSessionID(c)
}
