package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ReadinessHandler struct {
	config           *config.Config
	db               *database.DB
	storageAvailable bool
	runtimeAvailable bool
	logger           *zap.Logger
}

type readinessCheck struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Evidence any    `json:"evidence,omitempty"`
}

type readinessReport struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	Ready           bool             `json:"ready"`
	Blockers        int              `json:"blockers"`
	Warnings        int              `json:"warnings"`
	Checks          []readinessCheck `json:"checks"`
	EventChecksum   string           `json:"event_checksum"`
	ContentChecksum string           `json:"content_checksum"`
	EconomyChecksum string           `json:"economy_checksum"`
}

type createReleaseRequest struct {
	WaiveWarnings []string `json:"waive_warnings"`
}

func NewReadinessHandler(cfg *config.Config, db *database.DB, storageAvailable, runtimeAvailable bool, logger *zap.Logger) *ReadinessHandler {
	return &ReadinessHandler{config: cfg, db: db, storageAvailable: storageAvailable, runtimeAvailable: runtimeAvailable, logger: logger}
}

func (h *ReadinessHandler) Report(c *gin.Context) {
	report, err := h.buildReport(c.Request.Context())
	if err != nil {
		h.logger.Error("build readiness report", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build readiness report"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, report)
}

func (h *ReadinessHandler) CreateRelease(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var request createReleaseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid release request"})
		return
	}
	report, err := h.buildReport(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build readiness report"})
		return
	}
	if report.Blockers > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "release candidate has blockers", "report": report})
		return
	}
	waived := map[string]bool{}
	for _, id := range request.WaiveWarnings {
		waived[strings.TrimSpace(id)] = true
	}
	missing := []string{}
	for _, check := range report.Checks {
		if check.Status == "warning" && !waived[check.ID] {
			missing = append(missing, check.ID)
		}
	}
	if len(missing) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "every warning requires an explicit waiver", "missing_waivers": missing, "report": report})
		return
	}
	reportJSON, _ := json.Marshal(report)
	waivers := make([]string, 0, len(waived))
	for id := range waived {
		waivers = append(waivers, id)
	}
	sort.Strings(waivers)
	waiverJSON, _ := json.Marshal(waivers)
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create release candidate"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id uuid.UUID
	var sequence int64
	var createdAt time.Time
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO release_candidates
		(created_by, event_checksum, content_checksum, economy_checksum, report, waived_warnings)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)
		RETURNING id, sequence, created_at
	`, uid, report.EventChecksum, report.ContentChecksum, report.EconomyChecksum, reportJSON, waiverJSON).Scan(&id, &sequence, &createdAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create release candidate"})
		return
	}
	audit, _ := json.Marshal(gin.H{"sequence": sequence, "event_checksum": report.EventChecksum, "content_checksum": report.ContentChecksum, "economy_checksum": report.EconomyChecksum, "waived_warnings": waivers})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, 'release_candidate_created', 'release_candidate', $2, $3::jsonb, $4, $5)
	`, uid, id, audit, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to audit release candidate"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit release candidate"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "sequence": sequence, "created_at": createdAt, "report": report, "waived_warnings": waivers})
}

func (h *ReadinessHandler) ListReleases(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT rc.id, rc.sequence, rc.event_checksum, rc.content_checksum, rc.economy_checksum,
		       rc.report, rc.waived_warnings, rc.created_at, u.username
		FROM release_candidates rc JOIN users u ON u.id = rc.created_by
		ORDER BY rc.sequence DESC LIMIT 25
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load release candidates"})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var sequence int64
		var eventChecksum, contentChecksum, economyChecksum, username string
		var report, waivers json.RawMessage
		var createdAt time.Time
		if err := rows.Scan(&id, &sequence, &eventChecksum, &contentChecksum, &economyChecksum, &report, &waivers, &createdAt, &username); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load release candidates"})
			return
		}
		items = append(items, gin.H{"id": id, "sequence": sequence, "event_checksum": eventChecksum, "content_checksum": contentChecksum, "economy_checksum": economyChecksum, "report": report, "waived_warnings": waivers, "created_at": createdAt, "created_by": username})
	}
	c.JSON(http.StatusOK, gin.H{"release_candidates": items})
}

func (h *ReadinessHandler) buildReport(ctx context.Context) (readinessReport, error) {
	report := readinessReport{GeneratedAt: time.Now().UTC(), Checks: []readinessCheck{}}
	settings, settingsJSON, err := h.readinessSettings(ctx)
	if err != nil {
		return report, err
	}
	contentJSON, content, err := h.readinessContent(ctx)
	if err != nil {
		return report, err
	}
	report.EventChecksum = checksumBytes(settingsJSON)
	report.ContentChecksum = checksumBytes(contentJSON)
	report.EconomyChecksum = h.config.EconomyPolicyDescriptor().Checksum
	add := func(id, group, label, status, detail string, evidence any) {
		report.Checks = append(report.Checks, readinessCheck{ID: id, Group: group, Label: label, Status: status, Detail: detail, Evidence: evidence})
		if status == "blocker" {
			report.Blockers++
		} else if status == "warning" {
			report.Warnings++
		}
	}
	name := stringSetting(settings, "platform_name")
	slug := stringSetting(settings, "event.slug")
	timezone := stringSetting(settings, "event.timezone")
	identityReady := name != "" && importSlug.MatchString(slug) && timezone != ""
	add("event.identity", "Event", "Identity and timezone", status(identityReady, "blocker"), ternary(identityReady, "Event identity is complete.", "Set a valid name, slug and timezone."), gin.H{"name": name, "slug": slug, "timezone": timezone})
	start, startErr := time.Parse(time.RFC3339, stringSetting(settings, "event.start_at"))
	end, endErr := time.Parse(time.RFC3339, stringSetting(settings, "event.end_at"))
	scheduleReady := startErr == nil && endErr == nil && end.After(start)
	add("event.schedule", "Event", "Competition window", status(scheduleReady, "blocker"), ternary(scheduleReady, "Start and end form a valid UTC window.", "Set an end time after the start time."), nil)
	if scheduleReady && end.Before(time.Now()) {
		add("event.window_ended", "Event", "Competition window has ended", "warning", "The configured end time is in the past.", end)
	}
	mode := stringSetting(settings, "registration_mode")
	accessReady := mode == "open" || mode == "invite" || mode == "disabled"
	add("access.mode", "Access", "Registration mode", status(accessReady, "blocker"), ternary(accessReady, "Registration mode is supported.", "Select open, invite-only or closed registration."), mode)
	if mode == "invite" {
		var invites int
		if err := h.db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM invite_codes WHERE current_uses < max_uses AND (expires_at IS NULL OR expires_at > NOW())`).Scan(&invites); err != nil {
			return report, err
		}
		add("access.invites", "Access", "Active invitation", status(invites > 0, "blocker"), ternary(invites > 0, "At least one invite can be used.", "Create an active invite code."), invites)
	}
	add("content.categories", "Content", "Categories", status(content.Categories > 0, "blocker"), ternary(content.Categories > 0, "Challenge categories exist.", "Create at least one category."), content.Categories)
	add("content.published", "Content", "Published challenges", status(content.Published > 0, "blocker"), ternary(content.Published > 0, "Published participant content exists.", "Publish at least one validated challenge."), content.Published)
	add("content.flags", "Content", "Scoring path", status(content.MissingScoring == 0, "blocker"), ternary(content.MissingScoring == 0, "Every published challenge has a flag or grader.", "Published challenges are missing a flag or grader."), content.MissingScoring)
	add("content.runtime", "Infrastructure", "Runnable challenge definitions", status(content.MissingRuntime == 0, "blocker"), ternary(content.MissingRuntime == 0, "Published runtime challenges have an image or VM definition.", "Published challenges have incomplete runtime configuration."), content.MissingRuntime)
	add("platform.storage", "Infrastructure", "Storage backend", status(h.storageAvailable, "blocker"), ternary(h.storageAvailable, "The attachment and branding backend is connected.", "No storage backend is connected."), nil)
	add("platform.runtime", "Infrastructure", "Instance runtime", status(h.runtimeAvailable, "blocker"), ternary(h.runtimeAvailable, "An instance runtime is connected.", "No instance runtime is connected."), gin.H{"backend": h.config.Instancer.Backend, "orchestrator": h.config.Container.Orchestrator})
	pulseEnabled := boolSetting(settings, "market_pulse_enabled")
	economyEnabled := boolSetting(settings, "economy_mode")
	add("ledger.dependencies", "Ledger", "Feature dependencies", status(!pulseEnabled || economyEnabled, "blocker"), ternary(!pulseEnabled || economyEnabled, "Ledger-dependent features are consistent.", "Market Pulse requires the Ledger economy."), nil)
	contactReady := stringSetting(settings, "event.contact_email") != ""
	add("event.contact", "Operations", "Organizer contact", status(contactReady, "warning"), ternary(contactReady, "A public organizer contact is configured.", "Add a monitored organizer contact address."), nil)
	legalReady := stringSetting(settings, "event.rules_url") != "" && stringSetting(settings, "event.privacy_url") != "" && stringSetting(settings, "event.terms_url") != ""
	add("event.legal", "Operations", "Public policies", status(legalReady, "warning"), ternary(legalReady, "Rules, privacy and terms links are configured.", "Add rules, privacy and terms links for an enterprise event."), nil)
	add("operations.restore", "Operations", "Restore drill evidence", "warning", "Attach a recent verified backup and restore-drill record to the deployment runbook.", nil)
	setupComplete := boolSetting(settings, "event.setup_completed")
	add("event.setup", "Event", "Onboarding completion", status(setupComplete, "warning"), ternary(setupComplete, "The event setup checklist was completed.", "Complete the Event workspace checklist."), nil)
	report.Ready = report.Blockers == 0
	return report, nil
}

type readinessContentSummary struct {
	Categories     int
	Published      int
	MissingScoring int
	MissingRuntime int
}

func (h *ReadinessHandler) readinessContent(ctx context.Context) ([]byte, readinessContentSummary, error) {
	var content json.RawMessage
	var summary readinessContentSummary
	err := h.db.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(jsonb_agg(jsonb_build_object(
				'slug', c.slug, 'status', c.status, 'difficulty', c.difficulty, 'category', category.slug,
				'resource_type', c.resource_type, 'container_image', c.container_image, 'container_spec', c.container_spec,
				'scoring_mode', c.scoring_mode, 'arena_mode', c.arena_mode,
				'flags', (SELECT COUNT(*) FROM flags f WHERE f.challenge_id = c.id),
				'hints', (SELECT COUNT(*) FROM hints h WHERE h.challenge_id = c.id),
				'attachments', (SELECT COUNT(*) FROM challenge_attachments a WHERE a.challenge_id = c.id),
				'updated_at', c.updated_at
			) ORDER BY c.slug), '[]'::jsonb),
			(SELECT COUNT(*)::int FROM categories),
			COUNT(*) FILTER (WHERE c.status = 'published')::int,
			COUNT(*) FILTER (WHERE c.status = 'published' AND c.scoring_mode <> 'graded' AND c.arena_mode <> 'shared' AND NOT EXISTS (SELECT 1 FROM flags f WHERE f.challenge_id = c.id))::int,
			COUNT(*) FILTER (WHERE c.status = 'published' AND (
				(c.resource_type = 'vm' AND NOT EXISTS (
					SELECT 1 FROM challenge_resources cr WHERE cr.challenge_id = c.id AND cr.resource_type = 'vm' AND cr.is_active = TRUE
				)) OR (
					c.resource_type = 'docker' AND c.exposed_ports IS NOT NULL AND c.exposed_ports <> 'null'::jsonb AND c.exposed_ports <> '[]'::jsonb AND COALESCE(c.container_image, '') = '' AND c.container_spec IS NULL
				)
			))::int
		FROM challenges c LEFT JOIN categories category ON category.id = c.category_id
	`).Scan(&content, &summary.Categories, &summary.Published, &summary.MissingScoring, &summary.MissingRuntime)
	return content, summary, err
}

func (h *ReadinessHandler) readinessSettings(ctx context.Context) (map[string]json.RawMessage, []byte, error) {
	rows, err := h.db.Pool.Query(ctx, `SELECT key, value FROM platform_settings ORDER BY key`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	settings := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, nil, err
		}
		if exportSettingAllowlist[key] {
			settings[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(settings)
	return settings, encoded, err
}

func stringSetting(settings map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(settings[key], &value)
	return strings.TrimSpace(value)
}

func boolSetting(settings map[string]json.RawMessage, key string) bool {
	var value bool
	_ = json.Unmarshal(settings[key], &value)
	return value
}

func checksumBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func status(ready bool, failure string) string {
	if ready {
		return "pass"
	}
	return failure
}

func ternary(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}
