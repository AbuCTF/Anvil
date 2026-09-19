package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	RegistrationMode  string           `json:"registration_mode"`
	ScoringEnabled    bool             `json:"scoring_enabled"`
	ScoreboardEnabled bool             `json:"scoreboard_enabled"`
	DiscordWalkin     bool             `json:"discord_walkin"`
	EmailWalkin       bool             `json:"email_walkin"`
	ZeroPoolBaseURL   string           `json:"zeropool_base_url,omitempty"`
	ZeroPoolEventSlug string           `json:"zeropool_event_slug,omitempty"`
	TurnstileSiteKey  string           `json:"turnstile_site_key,omitempty"`
	ServerTime        time.Time        `json:"server_time"`
	Event             *publicEventInfo `json:"event,omitempty"`
}

func (h *PlatformHandler) GetInfo(c *gin.Context) {
	now := time.Now().UTC()
	response := platformInfoResponse{
		Name:              h.config.Platform.Name,
		Description:       h.config.Platform.Description,
		RegistrationMode:  h.config.Platform.RegistrationMode,
		ScoringEnabled:    h.config.Platform.ScoringEnabled,
		ScoreboardEnabled: h.config.Platform.ScoreboardEnabled,
		DiscordWalkin:     h.config.Discord.Enabled && h.config.Discord.ClientID != "",
		EmailWalkin:       strings.TrimRight(h.config.ZeroPool.BaseURL, "/") != "",
		ZeroPoolBaseURL:   strings.TrimRight(h.config.ZeroPool.BaseURL, "/"),
		ZeroPoolEventSlug: h.config.ZeroPool.EventSlug,
		TurnstileSiteKey:  h.config.ZeroPool.TurnstileSiteKey,
		ServerTime:        now,
	}

	startAt, endAt, err := h.eventWindow(c.Request.Context())
	if err != nil {
		h.logger.Warn("failed to load public event window", zap.Error(err))
	} else if startAt != nil && endAt != nil && eventClockVisible(now, *endAt) {
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

func (h *PlatformHandler) eventWindow(ctx context.Context) (*time.Time, *time.Time, error) {
	var startRaw, endRaw string
	err := h.db.Pool.QueryRow(ctx, `
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
