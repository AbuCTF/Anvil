package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// discord sign-in is LOGIN-ONLY: registration + email/discord verification all
// happen at the external registration site (zeropool). anvil never creates a
// zeropool account — it only looks one up by discord id and mirrors it locally.
// see CTF26/reg-anvil-handoff-contract.md.

var walkinHTTP = &http.Client{Timeout: 10 * time.Second}

// errZPNotRegistered means no zeropool participant is linked to this discord id
// for the event — the person must register at the registration site first.
var errZPNotRegistered = fmt.Errorf("not registered")

type zpLookupRequest struct {
	DiscordID string `json:"discord_id"`
	EventSlug string `json:"event_slug"`
	Create    bool   `json:"create"` // always false: anvil never registers
}

type zpParticipant struct {
	ParticipantID string `json:"participant_id"`
	Email         string `json:"email"`
	Username      string `json:"username"`
	EmailVerified bool   `json:"email_verified"`
}

// zpLookup finds the zeropool participant linked to a discord id (create:false).
// a 404 => errZPNotRegistered; the id it returns is stored as anvil's sso_subject.
func (h *AuthHandler) zpLookup(ctx context.Context, discordID string) (*zpParticipant, error) {
	base := strings.TrimRight(h.config.ZeroPool.BaseURL, "/")
	if base == "" || h.config.ZeroPool.APIKey == "" {
		return nil, fmt.Errorf("zeropool link not configured")
	}
	body, _ := json.Marshal(zpLookupRequest{DiscordID: discordID, EventSlug: h.config.ZeroPool.EventSlug, Create: false})
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/identity/provision", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("X-Anvil-Api-Key", h.config.ZeroPool.APIKey)
	resp, err := walkinHTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil, errZPNotRegistered
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zeropool lookup %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out zpParticipant
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("zeropool lookup: bad response")
	}
	if out.ParticipantID == "" {
		return nil, fmt.Errorf("zeropool lookup: no participant id")
	}
	return &out, nil
}

// DiscordAuthorize hands the client a discord oauth url carrying a signed,
// short-lived state that the callback verifies (stateless csrf guard).
func (h *AuthHandler) DiscordAuthorize(c *gin.Context) {
	if !h.config.Discord.Enabled || h.config.Discord.ClientID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "discord sign-in is not available"})
		return
	}
	state, err := h.signOAuthState()
	if err != nil {
		h.logger.Error("failed to sign discord state", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sign-in failed"})
		return
	}
	q := url.Values{
		"client_id":     {h.config.Discord.ClientID},
		"redirect_uri":  {h.config.Discord.RedirectURI},
		"response_type": {"code"},
		"scope":         {"identify email"},
		"state":         {state},
	}
	c.JSON(http.StatusOK, gin.H{"authorize_url": "https://discord.com/oauth2/authorize?" + q.Encode()})
}

type discordCallbackRequest struct {
	Code  string `json:"code" binding:"required"`
	State string `json:"state" binding:"required"`
}

// DiscordCallback exchanges the oauth code for the discord profile, looks up an
// existing (create:false) zeropool participant by discord id, and issues an anvil
// session. it never registers: an unknown discord => not_registered (go register
// at the registration site); an unverified account => verify_email.
func (h *AuthHandler) DiscordCallback(c *gin.Context) {
	if !h.config.Discord.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "discord sign-in is not available"})
		return
	}
	var req discordCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code and state are required"})
		return
	}
	if !h.verifyOAuthState(req.State) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sign-in expired, please try again"})
		return
	}

	ctx := c.Request.Context()
	profile, err := h.discordProfile(ctx, req.Code)
	if err != nil {
		h.logger.Warn("discord code exchange failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not reach Discord, please try again"})
		return
	}

	participant, err := h.zpLookup(ctx, profile.ID)
	if errors.Is(err, errZPNotRegistered) {
		c.JSON(http.StatusForbidden, gin.H{
			"code":         "not_registered",
			"error":        "you're not registered yet — sign up first, then come back",
			"register_url": h.config.Platform.RegisterURL,
		})
		return
	}
	if err != nil {
		h.logger.Error("zeropool lookup failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "sign-in is temporarily unavailable"})
		return
	}
	if !participant.EmailVerified {
		c.JSON(http.StatusForbidden, gin.H{
			"code":         "verify_email",
			"error":        "verify your email at the registration site, then sign in",
			"register_url": h.config.Platform.RegisterURL,
		})
		return
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin discord login tx", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sign-in failed"})
		return
	}
	defer tx.Rollback(ctx)
	email := participant.Email
	if email == "" {
		email = strings.ToLower(profile.Email)
	}
	h.provisionAndRespond(c, ctx, tx, participant.ParticipantID, email, profile.Username)
}

type discordProfileResult struct {
	ID       string
	Username string
	Email    string
	Verified bool
}

func (h *AuthHandler) discordProfile(ctx context.Context, code string) (*discordProfileResult, error) {
	form := url.Values{
		"client_id":     {h.config.Discord.ClientID},
		"client_secret": {h.config.Discord.ClientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {h.config.Discord.RedirectURI},
	}
	treq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	treq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tresp, err := walkinHTTP.Do(treq)
	if err != nil {
		return nil, err
	}
	defer tresp.Body.Close()
	if tresp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(tresp.Body, 1<<16))
		return nil, fmt.Errorf("token exchange %d: %s", tresp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tresp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("token exchange: no access token")
	}

	ureq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	if err != nil {
		return nil, err
	}
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := walkinHTTP.Do(ureq)
	if err != nil {
		return nil, err
	}
	defer uresp.Body.Close()
	if uresp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile fetch %d", uresp.StatusCode)
	}
	var profile discordProfileResult
	if err := json.NewDecoder(uresp.Body).Decode(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (h *AuthHandler) signOAuthState() (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "discord_oauth",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
	})
	return t.SignedString([]byte(h.config.JWT.Secret))
}

func (h *AuthHandler) verifyOAuthState(state string) bool {
	tok, err := jwt.ParseWithClaims(state, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(h.config.JWT.Secret), nil
	})
	if err != nil || !tok.Valid {
		return false
	}
	claims, ok := tok.Claims.(*jwt.RegisteredClaims)
	return ok && claims.Subject == "discord_oauth"
}
