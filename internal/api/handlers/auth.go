package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewAuthHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{
		config: cfg,
		db:     db,
		logger: logger,
	}
}

type RegisterRequest struct {
	Username   string  `json:"username" binding:"required,min=3,max=50"`
	Email      string  `json:"email" binding:"required,email"`
	Password   string  `json:"password" binding:"required,min=8"`
	InviteCode *string `json:"invite_code,omitempty"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type TokenAuthRequest struct {
	Token string `json:"token" binding:"required"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         *UserResponse `json:"user,omitempty"`
	Team         *TeamResponse `json:"team,omitempty"`
}

type UserResponse struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email,omitempty"`
	DisplayName *string   `json:"display_name,omitempty"`
	Role        string    `json:"role"`
	TotalScore  int       `json:"total_score"`
}

type TeamResponse struct {
	Token    string `json:"token"`
	TeamName string `json:"team_name"`
}

// Register handles user registration
func (h *AuthHandler) Register(c *gin.Context) {
	// Check registration mode
	var regMode string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		"SELECT value::text FROM platform_settings WHERE key = 'registration_mode'",
	).Scan(&regMode)
	if err != nil {
		regMode = "\"open\"" // Default to open
	}
	regMode = strings.Trim(regMode, "\"")

	if regMode == "disabled" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Registration is currently disabled",
		})
		return
	}

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// Check invite code if required
	if regMode == "invite" {
		if req.InviteCode == nil || *req.InviteCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invite code required",
			})
			return
		}

		// Validate invite code
		var codeID uuid.UUID
		var currentUses, maxUses int
		var expiresAt *time.Time
		err := h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT id, current_uses, max_uses, expires_at 
			 FROM invite_codes 
			 WHERE code = $1`,
			*req.InviteCode,
		).Scan(&codeID, &currentUses, &maxUses, &expiresAt)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid invite code",
			})
			return
		}

		if currentUses >= maxUses {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invite code has been fully used",
			})
			return
		}

		if expiresAt != nil && time.Now().After(*expiresAt) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invite code has expired",
			})
			return
		}

		// Increment usage
		_, err = h.db.Pool.Exec(c.Request.Context(),
			"UPDATE invite_codes SET current_uses = current_uses + 1 WHERE id = $1",
			codeID,
		)
		if err != nil {
			h.logger.Error("Failed to update invite code usage", zap.Error(err))
		}
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("Failed to hash password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to process registration",
		})
		return
	}

	// Create user
	var userID uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO users (username, email, password_hash, role, status)
		 VALUES ($1, $2, $3, 'user', 'active')
		 RETURNING id`,
		req.Username, req.Email, string(hashedPassword),
	).Scan(&userID)

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			if strings.Contains(err.Error(), "username") {
				c.JSON(http.StatusConflict, gin.H{
					"error": "Username already taken",
				})
			} else {
				c.JSON(http.StatusConflict, gin.H{
					"error": "Email already registered",
				})
			}
			return
		}
		h.logger.Error("Failed to create user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create account",
		})
		return
	}

	// Generate tokens
	tokens, err := h.generateTokens(userID, req.Username, "user", "user")
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete registration",
		})
		return
	}

	// Log audit
	h.logAudit(c, userID, "user.registered", "user", userID)

	c.JSON(http.StatusCreated, AuthResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    int(h.config.JWT.AccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User: &UserResponse{
			ID:         userID,
			Username:   req.Username,
			Email:      req.Email,
			Role:       "user",
			TotalScore: 0,
		},
	})
}

// Login handles user login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	// Find user
	var userID uuid.UUID
	var username, email, passwordHash, role, status string
	var displayName *string
	var totalScore int

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, username, email, password_hash, role, status, display_name, total_score
		 FROM users
		 WHERE username = $1 OR email = $1`,
		req.Username,
	).Scan(&userID, &username, &email, &passwordHash, &role, &status, &displayName, &totalScore)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials",
		})
		return
	}

	if status != "active" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Account is " + status,
		})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials",
		})
		return
	}

	// Update last login
	_, err = h.db.Pool.Exec(c.Request.Context(),
		"UPDATE users SET last_login_at = NOW(), last_login_ip = $1 WHERE id = $2",
		c.ClientIP(), userID,
	)
	if err != nil {
		h.logger.Warn("Failed to update last login", zap.Error(err))
	}

	// Generate tokens
	tokens, err := h.generateTokens(userID, username, role, "user")
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete login",
		})
		return
	}

	// Log audit
	h.logAudit(c, userID, "user.login", "user", userID)

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    int(h.config.JWT.AccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User: &UserResponse{
			ID:          userID,
			Username:    username,
			Email:       email,
			DisplayName: displayName,
			Role:        role,
			TotalScore:  totalScore,
		},
	})
}

// TokenAuth handles team token authentication
func (h *AuthHandler) TokenAuth(c *gin.Context) {
	var req TokenAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	// Find team token
	var tokenID uuid.UUID
	var teamName string
	var currentUses, maxUses int
	var expiresAt *time.Time

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, team_name, current_uses, max_uses, expires_at
		 FROM team_tokens
		 WHERE token = $1`,
		req.Token,
	).Scan(&tokenID, &teamName, &currentUses, &maxUses, &expiresAt)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid team token",
		})
		return
	}

	if currentUses >= maxUses {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Team token has reached maximum uses",
		})
		return
	}

	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Team token has expired",
		})
		return
	}

	// Create session
	sessionToken := generateSecureToken(32)
	sessionExpiry := time.Now().Add(24 * time.Hour)

	var sessionID uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO sessions (token_id, session_token, ip_address, user_agent, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		tokenID, sessionToken, c.ClientIP(), c.Request.UserAgent(), sessionExpiry,
	).Scan(&sessionID)

	if err != nil {
		h.logger.Error("Failed to create session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
		})
		return
	}

	// Increment token usage
	_, err = h.db.Pool.Exec(c.Request.Context(),
		"UPDATE team_tokens SET current_uses = current_uses + 1 WHERE id = $1",
		tokenID,
	)
	if err != nil {
		h.logger.Warn("Failed to update token usage", zap.Error(err))
	}

	// Generate JWT for session
	claims := middleware.Claims{
		SessionID: sessionID,
		Username:  teamName,
		Role:      "user",
		TokenType: "team",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(h.config.JWT.AccessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    h.config.JWT.Issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString([]byte(h.config.JWT.Secret))
	if err != nil {
		h.logger.Error("Failed to sign token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create access token",
		})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken: accessToken,
		ExpiresIn:   int(h.config.JWT.AccessExpiry.Seconds()),
		TokenType:   "Bearer",
		Team: &TeamResponse{
			Token:    req.Token,
			TeamName: teamName,
		},
	})
}

// RefreshToken handles token refresh
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Refresh token required",
		})
		return
	}

	// Hash the refresh token to compare with stored hash
	tokenHash := hashToken(req.RefreshToken)

	var userID uuid.UUID
	var expiresAt time.Time
	var revoked bool

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, expires_at, revoked
		 FROM refresh_tokens
		 WHERE token_hash = $1`,
		tokenHash,
	).Scan(&userID, &expiresAt, &revoked)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid refresh token",
		})
		return
	}

	if revoked {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Refresh token has been revoked",
		})
		return
	}

	if time.Now().After(expiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Refresh token has expired",
		})
		return
	}

	// Get user info
	var username, role string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		"SELECT username, role FROM users WHERE id = $1",
		userID,
	).Scan(&username, &role)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "User not found",
		})
		return
	}

	// Revoke old refresh token
	_, err = h.db.Pool.Exec(c.Request.Context(),
		"UPDATE refresh_tokens SET revoked = true WHERE token_hash = $1",
		tokenHash,
	)
	if err != nil {
		h.logger.Warn("Failed to revoke old refresh token", zap.Error(err))
	}

	// Generate new tokens
	tokens, err := h.generateTokens(userID, username, role, "user")
	if err != nil {
		h.logger.Error("Failed to generate new tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to refresh tokens",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokens.access,
		"refresh_token": tokens.refresh,
		"expires_in":    int(h.config.JWT.AccessExpiry.Seconds()),
		"token_type":    "Bearer",
	})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c *gin.Context) {
	// Get token from header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusOK, gin.H{
			"message": "Logged out",
		})
		return
	}

	// Could add token to blocklist here if needed

	c.JSON(http.StatusOK, gin.H{
		"message": "Logged out",
	})
}

type tokenPair struct {
	access  string
	refresh string
}

func (h *AuthHandler) generateTokens(userID uuid.UUID, username, role, tokenType string) (*tokenPair, error) {
	// Generate access token
	claims := middleware.Claims{
		UserID:    userID,
		Username:  username,
		Role:      role,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(h.config.JWT.AccessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    h.config.JWT.Issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString([]byte(h.config.JWT.Secret))
	if err != nil {
		return nil, err
	}

	// Generate refresh token
	refreshToken := generateSecureToken(32)
	refreshHash := hashToken(refreshToken)

	// Store refresh token
	_, err = h.db.Pool.Exec(context.Background(),
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, refreshHash, time.Now().Add(h.config.JWT.RefreshExpiry),
	)
	if err != nil {
		return nil, err
	}

	return &tokenPair{
		access:  accessToken,
		refresh: refreshToken,
	}, nil
}

func (h *AuthHandler) logAudit(c *gin.Context, userID uuid.UUID, action, entityType string, entityID uuid.UUID) {
	_, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO audit_log (user_id, action, entity_type, entity_id, ip_address, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, c.ClientIP(), c.Request.UserAgent(),
	)
	if err != nil {
		h.logger.Warn("Failed to log audit", zap.Error(err))
	}
}

func generateSecureToken(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func hashToken(token string) string {
	// Simple hash - in production use a proper hash function
	hash := make([]byte, 32)
	copy(hash, token)
	return hex.EncodeToString(hash)
}
