package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ExpiresIn    int           `json:"expires_in"`
	TokenType    string        `json:"token_type"`
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
	Rank        int       `json:"rank"`
}

type TeamResponse struct {
	Token    string `json:"token"`
	TeamName string `json:"team_name"`
}

const maxAuthRequestBytes = 16 << 10

// Register handles user registration
func (h *AuthHandler) Register(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthRequestBytes)
	regMode, err := h.registrationMode(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to load registration mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Registration is temporarily unavailable"})
		return
	}

	if regMode == "disabled" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Registration is currently disabled",
		})
		return
	}
	if regMode == "token" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Registration requires a team token",
		})
		return
	}

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}
	if len([]byte(req.Password)) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at most 72 bytes"})
		return
	}

	// An invite is checked authoritatively after the password is hashed, inside
	// the same transaction that creates the account and its refresh token.
	if regMode == "invite" {
		if req.InviteCode == nil || *req.InviteCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invite code required",
			})
			return
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

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("Failed to start registration transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create account",
		})
		return
	}
	defer tx.Rollback(ctx)

	var inviteCodeID uuid.UUID
	if regMode == "invite" {
		var currentUses, maxUses int
		var expiresAt *time.Time
		err = tx.QueryRow(ctx,
			`SELECT id, COALESCE(current_uses, 0), COALESCE(max_uses, 1), expires_at
			 FROM invite_codes
			 WHERE code = $1
			 FOR UPDATE`,
			*req.InviteCode,
		).Scan(&inviteCodeID, &currentUses, &maxUses, &expiresAt)

		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid invite code",
			})
			return
		}
		if err != nil {
			h.logger.Error("Failed to validate invite code", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to process registration",
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
	}

	// Create user
	var userID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, status)
		 VALUES ($1, $2, $3, 'user', 'active')
		 RETURNING id`,
		req.Username, req.Email, string(hashedPassword),
	).Scan(&userID)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "username") {
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

	if regMode == "invite" {
		result, err := tx.Exec(ctx,
			`UPDATE invite_codes
			 SET current_uses = COALESCE(current_uses, 0) + 1
			 WHERE id = $1
			   AND COALESCE(current_uses, 0) < COALESCE(max_uses, 1)`,
			inviteCodeID,
		)
		if err != nil {
			h.logger.Error("Failed to update invite code usage", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to create account",
			})
			return
		}
		if result.RowsAffected() != 1 {
			h.logger.Error("Invite code update affected an unexpected number of rows",
				zap.Int64("rows_affected", result.RowsAffected()))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to create account",
			})
			return
		}
	}

	// Generate tokens
	tokens, err := h.generateTokensWithStore(ctx, tx, userID, req.Username, "user", "user")
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete registration",
		})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("Failed to commit registration", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete registration",
		})
		return
	}

	// Log audit
	h.logAudit(c, userID, "user.registered", "user", userID)
	rank, err := currentUserRank(c.Request.Context(), h.db, userID)
	if err != nil {
		h.logger.Warn("Failed to load rank after registration", zap.String("user_id", userID.String()), zap.Error(err))
	}

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
			Rank:       rank,
		},
	})
}

func (h *AuthHandler) registrationMode(ctx context.Context) (string, error) {
	fallback := "open"
	if h.config != nil && strings.TrimSpace(h.config.Platform.RegistrationMode) != "" {
		fallback = strings.ToLower(strings.TrimSpace(h.config.Platform.RegistrationMode))
	}

	var mode string
	err := h.db.Pool.QueryRow(ctx,
		`SELECT value #>> '{}' FROM platform_settings WHERE key = 'registration_mode'`,
	).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		mode = fallback
	} else if err != nil {
		return "", fmt.Errorf("query registration mode: %w", err)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !isRegistrationMode(mode) {
		return "", fmt.Errorf("invalid registration mode %q", mode)
	}
	return mode, nil
}

func isRegistrationMode(mode string) bool {
	switch mode {
	case "open", "invite", "token", "disabled":
		return true
	default:
		return false
	}
}

// Login handles user login
func (h *AuthHandler) Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthRequestBytes)
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}
	if len([]byte(req.Password)) > 72 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
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

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials",
		})
		return
	}
	if err != nil {
		h.logger.Error("Failed to load account during login", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Login is temporarily unavailable"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
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

	// Update last login
	_, err = h.db.Pool.Exec(c.Request.Context(),
		"UPDATE users SET last_login_at = NOW(), last_login_ip = $1 WHERE id = $2",
		c.ClientIP(), userID,
	)
	if err != nil {
		h.logger.Warn("Failed to update last login", zap.Error(err))
	}

	// Generate tokens
	tokens, err := h.generateTokens(c.Request.Context(), userID, username, role, "user")
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete login",
		})
		return
	}

	// Log audit
	h.logAudit(c, userID, "user.login", "user", userID)
	rank, err := currentUserRank(c.Request.Context(), h.db, userID)
	if err != nil {
		h.logger.Warn("Failed to load rank during login", zap.String("user_id", userID.String()), zap.Error(err))
	}

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
			Rank:        rank,
		},
	})
}

// TokenAuth handles team token authentication
func (h *AuthHandler) TokenAuth(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthRequestBytes)
	var req TokenAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}
	if len(req.Token) > 255 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid team token"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("Failed to start team authentication transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
		})
		return
	}
	defer tx.Rollback(ctx)

	// Lock the team token until its session and usage count are committed.
	var tokenID uuid.UUID
	var teamName string
	var currentUses, maxUses int
	var expiresAt *time.Time

	err = tx.QueryRow(ctx,
		`SELECT id, team_name, COALESCE(current_uses, 0), COALESCE(max_uses, 1), expires_at
		 FROM team_tokens
		 WHERE token = $1
		 FOR UPDATE`,
		req.Token,
	).Scan(&tokenID, &teamName, &currentUses, &maxUses, &expiresAt)

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid team token",
		})
		return
	}
	if err != nil {
		h.logger.Error("Failed to validate team token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
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
	sessionToken, err := generateSecureToken(32)
	if err != nil {
		h.logger.Error("Failed to generate session token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}
	sessionExpiry := time.Now().Add(24 * time.Hour)

	var sessionID uuid.UUID
	err = tx.QueryRow(ctx,
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

	// Increment defensively as well as holding the row lock, so the database
	// cannot commit a use beyond the configured limit.
	result, err := tx.Exec(ctx,
		`UPDATE team_tokens
		 SET current_uses = COALESCE(current_uses, 0) + 1
		 WHERE id = $1
		   AND COALESCE(current_uses, 0) < COALESCE(max_uses, 1)`,
		tokenID,
	)
	if err != nil {
		h.logger.Error("Failed to update token usage", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
		})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("Team token update affected an unexpected number of rows",
			zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
		})
		return
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
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("Failed to commit team authentication", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create session",
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthRequestBytes)
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Refresh token required",
		})
		return
	}
	if len(req.RefreshToken) > 512 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("Failed to start refresh transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to refresh tokens",
		})
		return
	}
	defer tx.Rollback(ctx)

	// Hash the refresh token to compare with stored hash. Token rotation is kept
	// in this transaction so a failed revoke cannot leave the old token usable.
	tokenHash := hashToken(req.RefreshToken)

	var userID uuid.UUID
	var expiresAt time.Time
	var revoked bool

	err = tx.QueryRow(ctx,
		`SELECT user_id, expires_at, revoked
		 FROM refresh_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`,
		tokenHash,
	).Scan(&userID, &expiresAt, &revoked)

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid refresh token",
		})
		return
	}
	if err != nil {
		h.logger.Error("Failed to load refresh token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh tokens"})
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
	var username, role, status string
	err = tx.QueryRow(ctx,
		"SELECT username, role, status FROM users WHERE id = $1",
		userID,
	).Scan(&username, &role, &status)

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "User not found",
		})
		return
	}
	if err != nil {
		h.logger.Error("Failed to load user during token refresh", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh tokens"})
		return
	}
	if status != "active" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Account is " + status,
		})
		return
	}

	// Revoke old refresh token. The row lock from the lookup plus the conditional
	// update prevents two concurrent refreshes from both rotating one token.
	result, err := tx.Exec(ctx,
		"UPDATE refresh_tokens SET revoked = true WHERE token_hash = $1 AND revoked = false",
		tokenHash,
	)
	if err != nil {
		h.logger.Error("Failed to revoke old refresh token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to refresh tokens",
		})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("Refresh token revoke affected an unexpected number of rows",
			zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to refresh tokens",
		})
		return
	}

	// Generate new tokens
	tokens, err := h.generateTokensWithStore(ctx, tx, userID, username, role, "user")
	if err != nil {
		h.logger.Error("Failed to generate new tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to refresh tokens",
		})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("Failed to commit refreshed tokens", zap.Error(err))
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

// ssoClaims are the claims Anvil expects on a ZeroPool -> Anvil handoff token
// (model B). ZeroPool signs (HS256, shared secret); Anvil verifies + exchanges
// for an Anvil session. `sub` = the stable ZeroPool participant id.
type ssoClaims struct {
	Email         string `json:"email"`
	Username      string `json:"username"`
	EmailVerified bool   `json:"email_verified"`
	EventSlug     string `json:"event_slug,omitempty"`
	jwt.RegisteredClaims
}

// uniqueUsername picks an available Anvil username for a provisioned SSO user,
// preferring the token's username (or the email local-part), appending a short
// random suffix on collision. The users.username UNIQUE constraint is the backstop.
func (h *AuthHandler) uniqueUsername(ctx context.Context, tx pgx.Tx, preferred, email string) string {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = strings.Split(email, "@")[0]
	}
	clip := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n])
		}
		return s
	}
	base = clip(base, 40)
	if len([]rune(base)) < 3 {
		base += "usr"
	}
	candidate := base
	for i := 0; i < 8; i++ {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE lower(username) = lower($1))`, candidate,
		).Scan(&exists); err != nil {
			break
		}
		if !exists {
			return candidate
		}
		suffix, _ := generateSecureToken(2) // 4 hex chars
		candidate = clip(base, 44) + "-" + suffix
	}
	return candidate
}

// SSOLogin verifies a ZeroPool-signed handoff token, links or provisions the
// Anvil account, and issues an Anvil session. Gated by sso.enabled.
func (h *AuthHandler) SSOLogin(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthRequestBytes)
	if !h.config.SSO.Enabled || strings.TrimSpace(h.config.SSO.SharedSecret) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO is not enabled"})
		return
	}
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}

	var claims ssoClaims
	tok, err := jwt.ParseWithClaims(req.Token, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(h.config.SSO.SharedSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid SSO token"})
		return
	}

	if h.config.SSO.Issuer != "" {
		if iss, _ := claims.GetIssuer(); iss != h.config.SSO.Issuer {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid SSO token issuer"})
			return
		}
	}
	if h.config.SSO.Audience != "" {
		aud, _ := claims.GetAudience()
		ok := false
		for _, a := range aud {
			if a == h.config.SSO.Audience {
				ok = true
				break
			}
		}
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid SSO token audience"})
			return
		}
	}

	subject, _ := claims.GetSubject()
	subject = strings.TrimSpace(subject)
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if subject == "" || email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSO token missing subject or email"})
		return
	}
	// Anvil enforces "must be verified"; ZeroPool owns the verification state
	// (email-link or GitHub-verified) and only sets this true when appropriate.
	if !claims.EmailVerified {
		c.JSON(http.StatusForbidden, gin.H{"error": "email is not verified; verify on ZeroPool first"})
		return
	}
	expiresAt, _ := claims.GetExpirationTime()
	if expiresAt == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "SSO token missing expiry"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin SSO tx", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
		return
	}
	defer tx.Rollback(ctx)

	// Replay guard: a token's jti is single-use.
	if jti := strings.TrimSpace(claims.ID); jti != "" {
		ct, err := tx.Exec(ctx,
			`INSERT INTO sso_used_tokens (jti, expires_at) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`,
			jti, expiresAt.Time)
		if err != nil {
			h.logger.Error("failed to record SSO jti", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
			return
		}
		if ct.RowsAffected() == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "SSO token already used"})
			return
		}
	}

	var userID uuid.UUID
	var username, role string
	var displayName *string
	var totalScore int

	// 1) existing SSO-linked user
	err = tx.QueryRow(ctx,
		`SELECT id, username, role, display_name, total_score FROM users WHERE sso_subject = $1`, subject,
	).Scan(&userID, &username, &role, &displayName, &totalScore)

	// 2) link an existing user by email (first SSO for a pre-existing account)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`UPDATE users SET sso_subject = $1, email_verified = TRUE, updated_at = NOW()
			 WHERE lower(email) = $2 AND sso_subject IS NULL
			 RETURNING id, username, role, display_name, total_score`, subject, email,
		).Scan(&userID, &username, &role, &displayName, &totalScore)
	}

	// 3) provision a new user
	if errors.Is(err, pgx.ErrNoRows) {
		username = h.uniqueUsername(ctx, tx, claims.Username, email)
		role = "user"
		randPw, _ := generateSecureToken(24)
		hashed, hErr := bcrypt.GenerateFromPassword([]byte(randPw), bcrypt.DefaultCost)
		if hErr != nil {
			h.logger.Error("failed to hash SSO password", zap.Error(hErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
			return
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role, status, email_verified, sso_subject)
			 VALUES ($1, $2, $3, 'user', 'active', TRUE, $4)
			 RETURNING id, total_score`, username, email, string(hashed), subject,
		).Scan(&userID, &totalScore)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				c.JSON(http.StatusConflict, gin.H{"error": "an account with that email or username already exists; sign in normally"})
				return
			}
			h.logger.Error("failed to provision SSO user", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
			return
		}
		displayName = nil
	} else if err != nil {
		h.logger.Error("failed to resolve SSO user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
		return
	}

	tokens, err := h.generateTokensWithStore(ctx, tx, userID, username, role, "user")
	if err != nil {
		h.logger.Error("failed to issue SSO session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit SSO session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SSO failed"})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    int(h.config.JWT.AccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User: &UserResponse{
			ID: userID, Username: username, Email: email,
			DisplayName: displayName, Role: role, TotalScore: totalScore, Rank: 0,
		},
	})
}

func (h *AuthHandler) generateTokens(ctx context.Context, userID uuid.UUID, username, role, tokenType string) (*tokenPair, error) {
	return h.generateTokensWithStore(ctx, h.db.Pool, userID, username, role, tokenType)
}

type tokenStore interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
}

func (h *AuthHandler) generateTokensWithStore(ctx context.Context, store tokenStore, userID uuid.UUID, username, role, tokenType string) (*tokenPair, error) {
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
	refreshToken, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshHash := hashToken(refreshToken)

	// Store refresh token
	_, err = store.Exec(ctx,
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

func generateSecureToken(length int) (string, error) {
	if length < 1 {
		return "", errors.New("token length must be positive")
	}
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
