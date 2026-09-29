package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type DataHandler struct {
	db     *database.DB
	logger *zap.Logger
}

type exportCollection struct {
	Headers []string         `json:"headers"`
	Rows    []map[string]any `json:"rows"`
}

type exportManifestFile struct {
	Path     string `json:"path"`
	Entity   string `json:"entity"`
	Format   string `json:"format"`
	Records  int    `json:"records"`
	Checksum string `json:"sha256"`
}

type exportManifest struct {
	Format       string               `json:"format"`
	Version      int                  `json:"version"`
	CreatedAt    time.Time            `json:"created_at"`
	CreatedBy    uuid.UUID            `json:"created_by"`
	Anonymized   bool                 `json:"anonymized"`
	Secrets      string               `json:"secrets"`
	Excluded     []string             `json:"excluded"`
	Files        []exportManifestFile `json:"files"`
	PolicyNotice string               `json:"policy_notice"`
}

var exportEntities = []string{"settings", "categories", "challenges", "users", "teams", "team_members"}

var exportSettingAllowlist = map[string]bool{
	"platform_name": true, "platform_description": true, "registration_mode": true,
	"scoring_enabled": true, "scoreboard_enabled": true, "teams_mode": true,
	"economy_mode": true, "arena_enabled": true, "market_pulse_enabled": true,
	"event.slug": true, "event.timezone": true, "event.contact_email": true,
	"event.rules_url": true, "event.privacy_url": true, "event.terms_url": true,
	"event.start_at": true, "event.end_at": true, "event.profile_managed": true,
	"event.setup_completed": true, "scoreboard.history_end_at": true,
	"participants.team_creation": true, "participants.team_join": true,
	"participants.default_team_size": true, "participants.max_teams": true,
	"participants.allowed_email_domains": true, "notifications.sound_allowed": true,
	"branding.accent": true,
}

func NewDataHandler(db *database.DB, logger *zap.Logger) *DataHandler {
	return &DataHandler{db: db, logger: logger}
}

func (h *DataHandler) Summary(c *gin.Context) {
	var users, teams, categories, challenges, solves, pending int
	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT
			(SELECT COUNT(*)::int FROM users),
			(SELECT COUNT(*)::int FROM teams),
			(SELECT COUNT(*)::int FROM categories),
			(SELECT COUNT(*)::int FROM challenges),
			(SELECT COUNT(*)::int FROM solves),
			(SELECT COUNT(*)::int FROM data_import_jobs WHERE status = 'pending' AND expires_at > NOW())
	`).Scan(&users, &teams, &categories, &challenges, &solves, &pending)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load data summary"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users, "teams": teams, "categories": categories, "challenges": challenges, "solves": solves, "pending_imports": pending})
}

func (h *DataHandler) Export(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "bundle")))
	if format != "bundle" && format != "json" && format != "csv" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be bundle, json, or csv"})
		return
	}
	entities, err := requestedExportEntities(c.Query("entities"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if format == "csv" && len(entities) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV export requires exactly one entity"})
		return
	}
	anonymized := c.Query("anonymize") == "true"
	collections := make(map[string]exportCollection, len(entities))
	for _, entity := range entities {
		collection, fetchErr := h.fetchExportEntity(c.Request.Context(), entity)
		if fetchErr != nil {
			h.logger.Error("export data", zap.String("entity", entity), zap.Error(fetchErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export " + entity})
			return
		}
		if anonymized {
			anonymizeCollection(entity, &collection)
		}
		collections[entity] = collection
	}
	if err := logAdminAction(h.db, c, uid.String(), "data_exported", "data_export", "", map[string]any{"format": format, "entities": entities, "anonymized": anonymized}); err != nil {
		h.logger.Warn("audit data export", zap.Error(err))
	}
	filename := "anvil-export-" + time.Now().UTC().Format("20060102T150405Z")
	switch format {
	case "csv":
		entity := entities[0]
		data, err := encodeCollectionCSV(collections[entity])
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode export"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, filename, entity))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
	case "json":
		payload := gin.H{"format": "anvil-data", "version": 1, "created_at": time.Now().UTC(), "anonymized": anonymized, "data": collections}
		data, _ := json.MarshalIndent(payload, "", "  ")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, filename))
		c.Data(http.StatusOK, "application/json", data)
	default:
		data, err := encodeBundle(uid, anonymized, entities, collections)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode export"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.anvil.zip"`, filename))
		c.Data(http.StatusOK, "application/zip", data)
	}
}

func requestedExportEntities(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), exportEntities...), nil
	}
	valid := make(map[string]bool, len(exportEntities))
	for _, entity := range exportEntities {
		valid[entity] = true
	}
	seen := map[string]bool{}
	entities := make([]string, 0)
	for _, entity := range strings.Split(raw, ",") {
		entity = strings.TrimSpace(entity)
		if !valid[entity] {
			return nil, fmt.Errorf("unsupported export entity %q", entity)
		}
		if !seen[entity] {
			seen[entity] = true
			entities = append(entities, entity)
		}
	}
	return entities, nil
}

func encodeBundle(uid uuid.UUID, anonymized bool, entities []string, collections map[string]exportCollection) ([]byte, error) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	manifest := exportManifest{
		Format: "anvil-event-bundle", Version: 1, CreatedAt: time.Now().UTC(), CreatedBy: uid,
		Anonymized: anonymized, Secrets: "excluded",
		Excluded:     []string{"password_hashes", "sessions", "refresh_tokens", "flags", "grader_secrets", "join_codes", "koth_tokens", "rpc_secrets", "provider_credentials", "active_instances"},
		PolicyNotice: "This bundle is a portable administrative export, not a byte-for-byte database backup.",
		Files:        []exportManifestFile{},
	}
	for _, entity := range entities {
		collection := collections[entity]
		jsonData, err := json.MarshalIndent(collection.Rows, "", "  ")
		if err != nil {
			return nil, err
		}
		csvData, err := encodeCollectionCSV(collection)
		if err != nil {
			return nil, err
		}
		for _, file := range []struct {
			path, format string
			data         []byte
		}{{"data/" + entity + ".json", "json", jsonData}, {"data/" + entity + ".csv", "csv", csvData}} {
			entry, err := writer.Create(file.path)
			if err != nil {
				return nil, err
			}
			if _, err := entry.Write(file.data); err != nil {
				return nil, err
			}
			sum := sha256.Sum256(file.data)
			manifest.Files = append(manifest.Files, exportManifestFile{Path: file.path, Entity: entity, Format: file.format, Records: len(collection.Rows), Checksum: hex.EncodeToString(sum[:])})
		}
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	entry, err := writer.Create("manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(manifestData); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func encodeCollectionCSV(collection exportCollection) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write(collection.Headers); err != nil {
		return nil, err
	}
	for _, record := range collection.Rows {
		row := make([]string, len(collection.Headers))
		for index, header := range collection.Headers {
			row[index] = csvSafe(exportString(record[header]))
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return output.Bytes(), writer.Error()
}

func exportString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

func csvSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

func anonymizeCollection(entity string, collection *exportCollection) {
	for _, row := range collection.Rows {
		switch entity {
		case "users":
			alias := pseudonym("user", exportString(row["username"]))
			row["username"], row["display_name"], row["email"] = alias, alias, ""
		case "teams":
			row["name"] = pseudonym("team", exportString(row["name"]))
		case "team_members":
			row["username"] = pseudonym("user", exportString(row["username"]))
			row["team_name"] = pseudonym("team", exportString(row["team_name"]))
		}
	}
}

func pseudonym(prefix, value string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + value))
	return prefix + "-" + hex.EncodeToString(sum[:6])
}

func (h *DataHandler) fetchExportEntity(ctx context.Context, entity string) (exportCollection, error) {
	switch entity {
	case "settings":
		rows, err := h.db.Pool.Query(ctx, `SELECT key, value, COALESCE(description, '') FROM platform_settings ORDER BY key`)
		if err != nil {
			return exportCollection{}, err
		}
		defer rows.Close()
		result := exportCollection{Headers: []string{"key", "value", "description"}, Rows: []map[string]any{}}
		for rows.Next() {
			var key, description string
			var value json.RawMessage
			if err := rows.Scan(&key, &value, &description); err != nil {
				return result, err
			}
			if exportSettingAllowlist[key] {
				result.Rows = append(result.Rows, map[string]any{"key": key, "value": string(value), "description": description})
			}
		}
		return result, rows.Err()
	case "categories":
		return h.queryExport(ctx, []string{"slug", "name", "description", "color", "icon", "sort_order"}, `SELECT slug, name, COALESCE(description, ''), COALESCE(color, ''), COALESCE(icon, ''), sort_order FROM categories ORDER BY sort_order, slug`)
	case "challenges":
		return h.queryExport(ctx, []string{"slug", "name", "description", "sub_description", "difficulty", "category_slug", "status", "author_name", "resource_type", "container_image", "container_tag", "cpu_limit", "memory_limit", "exposed_ports", "base_points", "release_date", "privesc", "scoring_mode"}, `
			SELECT c.slug, c.name, COALESCE(c.description, ''), COALESCE(c.sub_description, ''), c.difficulty::text,
			       COALESCE(category.slug, ''), c.status::text, COALESCE(c.author_name, ''), COALESCE(c.resource_type::text, ''),
			       c.container_image, c.container_tag, c.cpu_limit, c.memory_limit, c.exposed_ports::text,
			       c.base_points, c.release_date, c.privesc, c.scoring_mode
			FROM challenges c LEFT JOIN categories category ON category.id = c.category_id ORDER BY c.slug`)
	case "users":
		return h.queryExport(ctx, []string{"username", "email", "display_name", "role", "status", "email_verified", "created_at"}, `SELECT username, COALESCE(email, ''), COALESCE(display_name, ''), role::text, status::text, email_verified, created_at FROM users ORDER BY username`)
	case "teams":
		return h.queryExport(ctx, []string{"name", "max_members", "total_score", "created_at"}, `SELECT name, max_members, total_score, created_at FROM teams ORDER BY name`)
	case "team_members":
		return h.queryExport(ctx, []string{"username", "team_name"}, `SELECT u.username, t.name FROM users u JOIN teams t ON t.id = u.team_id ORDER BY t.name, u.username`)
	default:
		return exportCollection{}, fmt.Errorf("unsupported entity %q", entity)
	}
}

func (h *DataHandler) queryExport(ctx context.Context, headers []string, query string) (exportCollection, error) {
	rows, err := h.db.Pool.Query(ctx, query)
	if err != nil {
		return exportCollection{}, err
	}
	defer rows.Close()
	result := exportCollection{Headers: headers, Rows: []map[string]any{}}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return result, err
		}
		record := make(map[string]any, len(headers))
		for index, header := range headers {
			record[header] = values[index]
		}
		result.Rows = append(result.Rows, record)
	}
	return result, rows.Err()
}
