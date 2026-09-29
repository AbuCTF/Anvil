package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type publicEventInfo struct {
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
	VisibleUntil time.Time `json:"visible_until"`
	Phase        string    `json:"phase"`
}

const eventClockGracePeriod = 48 * time.Hour

type platformInfoResponse struct {
	Name               string                         `json:"name"`
	Description        string                         `json:"description"`
	Slug               string                         `json:"slug"`
	Timezone           string                         `json:"timezone"`
	ContactEmail       string                         `json:"contact_email,omitempty"`
	LogoURL            string                         `json:"logo_url,omitempty"`
	RulesURL           string                         `json:"rules_url,omitempty"`
	PrivacyURL         string                         `json:"privacy_url,omitempty"`
	TermsURL           string                         `json:"terms_url,omitempty"`
	Accent             string                         `json:"accent"`
	RegistrationMode   string                         `json:"registration_mode"`
	ScoringEnabled     bool                           `json:"scoring_enabled"`
	ScoreboardEnabled  bool                           `json:"scoreboard_enabled"`
	ArenaEnabled       bool                           `json:"arena_enabled"`
	EconomyEnabled     bool                           `json:"economy_enabled"`
	MarketPulseEnabled bool                           `json:"market_pulse_enabled"`
	NotificationSound  bool                           `json:"notification_sound_allowed"`
	EconomyPolicy      config.EconomyPolicyDescriptor `json:"economy_policy"`
	TeamsMode          bool                           `json:"teams_mode"`
	TeamCreation       string                         `json:"team_creation_policy"`
	TeamJoin           string                         `json:"team_join_policy"`
	VPNEnabled         bool                           `json:"vpn_enabled"`
	SSOEnabled         bool                           `json:"sso_enabled"`
	DiscordWalkin      bool                           `json:"discord_walkin"`
	RegisterURL        string                         `json:"register_url,omitempty"`
	ServerTime         time.Time                      `json:"server_time"`
	Event              *publicEventInfo               `json:"event,omitempty"`
}

func (h *PlatformHandler) GetInfo(c *gin.Context) {
	now := time.Now().UTC()
	ctx := c.Request.Context()
	// nav-gating flags; on a read error we default the feature off (fail closed).
	arena, err := boolSettingOrDefault(ctx, h.db, "arena_enabled", false)
	if err != nil {
		h.logger.Warn("failed to read arena_enabled", zap.Error(err))
	}
	teams, err := isTeamsMode(ctx, h.db)
	if err != nil {
		h.logger.Warn("failed to read teams_mode", zap.Error(err))
	}
	// economy_enabled gates the credits UI + the /economy/me poll; fail closed.
	economy, err := boolSettingOrDefault(ctx, h.db, "economy_mode", false)
	if err != nil {
		h.logger.Warn("failed to read economy_mode", zap.Error(err))
	}
	pulse, err := boolSettingOrDefault(ctx, h.db, "market_pulse_enabled", false)
	if err != nil {
		h.logger.Warn("failed to read market_pulse_enabled", zap.Error(err))
	}
	scoring, err := boolSettingOrDefault(ctx, h.db, "scoring_enabled", h.config.Platform.ScoringEnabled)
	if err != nil {
		h.logger.Warn("failed to read scoring_enabled", zap.Error(err))
	}
	scoreboard, err := boolSettingOrDefault(ctx, h.db, "scoreboard_enabled", h.config.Platform.ScoreboardEnabled)
	if err != nil {
		h.logger.Warn("failed to read scoreboard_enabled", zap.Error(err))
	}
	notificationSound, err := boolSettingOrDefault(ctx, h.db, "notifications.sound_allowed", false)
	if err != nil {
		h.logger.Warn("failed to read notifications.sound_allowed", zap.Error(err))
	}
	teamCreation, err := textSettingOrDefault(ctx, h.db, "participants.team_creation", "open")
	if err != nil {
		h.logger.Warn("failed to read participants.team_creation", zap.Error(err))
	}
	teamJoin, err := textSettingOrDefault(ctx, h.db, "participants.team_join", "code")
	if err != nil {
		h.logger.Warn("failed to read participants.team_join", zap.Error(err))
	}
	registrationMode := h.config.Platform.RegistrationMode
	if effective, modeErr := (&AuthHandler{config: h.config, db: h.db}).registrationMode(ctx); modeErr != nil {
		h.logger.Warn("failed to read effective registration_mode", zap.Error(modeErr))
	} else {
		registrationMode = effective
	}
	profile, err := loadEventProfile(ctx, h.db, h.config.Platform.Name, h.config.Platform.Description)
	if err != nil {
		h.logger.Warn("failed to read event profile", zap.Error(err))
	}
	response := platformInfoResponse{
		Name:               profile.Name,
		Description:        profile.Description,
		Slug:               profile.Slug,
		Timezone:           profile.Timezone,
		ContactEmail:       profile.ContactEmail,
		LogoURL:            profile.LogoURL,
		RulesURL:           profile.RulesURL,
		PrivacyURL:         profile.PrivacyURL,
		TermsURL:           profile.TermsURL,
		Accent:             profile.Accent,
		RegistrationMode:   registrationMode,
		ScoringEnabled:     scoring,
		ScoreboardEnabled:  scoreboard,
		ArenaEnabled:       arena,
		EconomyEnabled:     economy,
		MarketPulseEnabled: pulse,
		NotificationSound:  notificationSound,
		EconomyPolicy:      h.config.EconomyPolicyDescriptor(),
		TeamsMode:          teams,
		TeamCreation:       teamCreation,
		TeamJoin:           teamJoin,
		VPNEnabled:         h.config.VPN.Enabled,
		SSOEnabled:         h.config.SSO.Enabled && strings.TrimSpace(h.config.SSO.SharedSecret) != "",
		DiscordWalkin:      h.config.Discord.Enabled && h.config.Discord.ClientID != "",
		RegisterURL:        h.config.Platform.RegisterURL,
		ServerTime:         now,
	}

	startAt, endAt, err := h.eventWindow(c.Request.Context())
	if err != nil {
		h.logger.Warn("failed to load public event window", zap.Error(err))
	} else if startAt != nil && endAt != nil {
		response.Event = &publicEventInfo{
			StartAt:      *startAt,
			EndAt:        *endAt,
			VisibleUntil: endAt.Add(eventClockGracePeriod),
			Phase:        eventPhase(now, *startAt, *endAt),
		}
	}

	// server timestamp corrects client clock drift; do not cache it.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}

type eventProfile struct {
	Name, Description, Slug, Timezone, ContactEmail, LogoURL, RulesURL, PrivacyURL, TermsURL, Accent string
}

func loadEventProfile(ctx context.Context, db *database.DB, defaultName, defaultDescription string) (eventProfile, error) {
	profile := eventProfile{Name: defaultName, Description: defaultDescription, Slug: "anvil-event", Timezone: "UTC"}
	var logoKey string
	var managed bool
	err := db.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'platform_name'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'platform_description'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.slug'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.timezone'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.contact_email'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'branding.logo_key'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.rules_url'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.privacy_url'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.terms_url'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'branding.accent'), 'cyan'),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.profile_managed'), 'false')::boolean
		FROM platform_settings
		WHERE key IN ('platform_name', 'platform_description', 'event.slug', 'event.timezone',
			'event.contact_email', 'branding.logo_key', 'event.rules_url', 'event.privacy_url', 'event.terms_url', 'branding.accent',
			'event.profile_managed')
	`).Scan(&profile.Name, &profile.Description, &profile.Slug, &profile.Timezone, &profile.ContactEmail,
		&logoKey, &profile.RulesURL, &profile.PrivacyURL, &profile.TermsURL, &profile.Accent, &managed)
	if err != nil {
		return profile, err
	}
	if !managed || profile.Name == "" {
		profile.Name = defaultName
	}
	if !managed {
		profile.Description = defaultDescription
	}
	if profile.Slug == "" {
		profile.Slug = "anvil-event"
	}
	if profile.Timezone == "" {
		profile.Timezone = "UTC"
	}
	if profile.Accent == "" {
		profile.Accent = "cyan"
	}
	if logoKey != "" {
		profile.LogoURL = "/api/v1/branding/logo?v=" + strings.TrimPrefix(logoKey, "branding/logos/")
	}
	return profile, nil
}

func (h *PlatformHandler) eventWindow(ctx context.Context) (*time.Time, *time.Time, error) {
	return loadEventWindow(ctx, h.db)
}

// loadEventWindow reads the configured event start/end (nil,nil when unset =>
// no event gate). Shared by /info and the challenge phase gate.
func loadEventWindow(ctx context.Context, db *database.DB) (*time.Time, *time.Time, error) {
	var startRaw, endRaw string
	err := db.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.start_at'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.end_at'), '')
		FROM platform_settings
		WHERE key IN ('event.start_at', 'event.end_at')
	`).Scan(&startRaw, &endRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("query event settings: %w", err)
	}
	return parseEventWindow(startRaw, endRaw)
}

// isStaff reports whether the caller is an admin or author (no DB access). Staff
// bypass the event-phase gate and can see/play draft challenges (preview).
func isStaff(c *gin.Context) bool {
	if role, ok := c.Get("role"); ok {
		if r, _ := role.(string); r == "admin" || r == "author" {
			return true
		}
	}
	return false
}

// eventPlayState returns the current event phase ("" when no window is set, else
// "scheduled"|"live"|"ended") and whether the caller is staff (admin/author),
// who bypass the gate so they can test challenges in any phase.
func eventPlayState(c *gin.Context, db *database.DB) (phase string, staff bool) {
	staff = isStaff(c)
	start, end, err := loadEventWindow(c.Request.Context(), db)
	if err != nil || start == nil || end == nil {
		return "", staff // no window (or a read error) => no gate
	}
	return eventPhase(time.Now().UTC(), *start, *end), staff
}

func parseEventWindow(startRaw, endRaw string) (*time.Time, *time.Time, error) {
	startRaw = strings.TrimSpace(startRaw)
	endRaw = strings.TrimSpace(endRaw)
	if startRaw == "" && endRaw == "" {
		return nil, nil, nil
	}
	if startRaw == "" || endRaw == "" {
		return nil, nil, errors.New("event start and end must both be configured")
	}
	startAt, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse event start: %w", err)
	}
	endAt, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse event end: %w", err)
	}
	if !endAt.After(startAt) {
		return nil, nil, errors.New("event end must be after event start")
	}
	startUTC, endUTC := startAt.UTC(), endAt.UTC()
	return &startUTC, &endUTC, nil
}

func eventPhase(now, startAt, endAt time.Time) string {
	switch {
	case now.Before(startAt):
		return "scheduled"
	case now.Before(endAt):
		return "live"
	default:
		return "ended"
	}
}

func eventClockVisible(now, endAt time.Time) bool {
	return now.Before(endAt.Add(eventClockGracePeriod))
}
