package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const maxImportBytes = 5 << 20
const maxImportRows = 5000

type importPreviewRequest struct {
	Entity     string `json:"entity"`
	Format     string `json:"format"`
	Mode       string `json:"mode"`
	SourceName string `json:"source_name"`
	Content    string `json:"content"`
}

type importApplyRequest struct {
	Checksum string `json:"checksum"`
}

type importIssue struct {
	Row     int    `json:"row"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type importRowPlan struct {
	Row    int    `json:"row"`
	Key    string `json:"key"`
	Action string `json:"action"`
}

type importPlan struct {
	Create        int             `json:"create"`
	Update        int             `json:"update"`
	Skip          int             `json:"skip"`
	Errors        []importIssue   `json:"errors"`
	Rows          []importRowPlan `json:"rows"`
	StateChecksum string          `json:"state_checksum"`
}

type importPayload struct {
	Rows []map[string]string `json:"rows"`
}

type importQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type importSpec struct {
	Headers  []string
	Required []string
	Key      string
}

var importSpecs = map[string]importSpec{
	"categories": {
		Headers:  []string{"slug", "name", "description", "color", "icon", "sort_order"},
		Required: []string{"slug", "name"}, Key: "slug",
	},
	"challenges": {
		Headers:  []string{"slug", "name", "description", "sub_description", "difficulty", "category_slug", "status", "author_name", "resource_type", "delivery_type", "container_image", "container_tag", "cpu_limit", "memory_limit", "exposed_ports", "base_points", "release_date", "privesc", "scoring_mode"},
		Required: []string{"slug", "name", "difficulty"}, Key: "slug",
	},
	"users": {
		Headers:  []string{"username", "email", "display_name", "role", "status", "email_verified"},
		Required: []string{"username"}, Key: "username",
	},
	"teams": {
		Headers: []string{"name", "max_members"}, Required: []string{"name"}, Key: "name",
	},
	"team_members": {
		Headers: []string{"username", "team_name"}, Required: []string{"username", "team_name"}, Key: "username",
	},
}

var importSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,198}[a-z0-9])?$`)
var importUsername = regexp.MustCompile(`^[A-Za-z0-9_-]{3,50}$`)
var importColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (h *DataHandler) Template(c *gin.Context) {
	entity := strings.TrimSpace(c.Param("entity"))
	spec, ok := importSpecs[entity]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unsupported import entity"})
		return
	}
	data, err := encodeCollectionCSV(exportCollection{Headers: spec.Headers, Rows: []map[string]any{}})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="anvil-%s-template.csv"`, entity))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (h *DataHandler) PreviewImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportBytes+(64<<10))
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var request importPreviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid import request"})
		return
	}
	request.Entity = strings.ToLower(strings.TrimSpace(request.Entity))
	request.Format = strings.ToLower(strings.TrimSpace(request.Format))
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.SourceName = strings.TrimSpace(request.SourceName)
	if _, ok := importSpecs[request.Entity]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported import entity"})
		return
	}
	if request.Format != "csv" && request.Format != "json" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv or json"})
		return
	}
	if request.Mode != "create" && request.Mode != "merge" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be create or merge"})
		return
	}
	if request.SourceName == "" || len(request.SourceName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_name is required"})
		return
	}
	if len(request.Content) == 0 || len(request.Content) > maxImportBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "import content must be between 1 byte and 5 MB"})
		return
	}
	rows, issues := parseImportContent(request.Entity, request.Format, []byte(request.Content))
	if len(rows) > maxImportRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": "imports are limited to 5000 rows"})
		return
	}
	if request.Entity == "teams" {
		var defaultSize int
		if err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT COALESCE((SELECT (value #>> '{}')::int FROM platform_settings WHERE key = 'participants.default_team_size'), 4)`).Scan(&defaultSize); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load participant policy"})
			return
		}
		for _, row := range rows {
			if row["max_members"] == "" {
				row["max_members"] = strconv.Itoa(defaultSize)
			}
		}
	}
	_, _ = h.db.Pool.Exec(c.Request.Context(), `UPDATE data_import_jobs SET status = 'expired', payload = '{"rows":[]}'::jsonb WHERE status = 'pending' AND expires_at <= NOW()`)
	plan := h.planImport(c.Request.Context(), h.db.Pool, request.Entity, request.Mode, rows, issues)
	payload, _ := json.Marshal(importPayload{Rows: rows})
	planJSON, _ := json.Marshal(plan)
	sum := sha256.Sum256([]byte(request.Content))
	checksum := hex.EncodeToString(sum[:])
	jobID := uuid.New()
	_, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO data_import_jobs
		(id, created_by, entity, source_format, import_mode, source_name, checksum, row_count, plan, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb)
	`, jobID, uid, request.Entity, request.Format, request.Mode, request.SourceName, checksum, len(rows), planJSON, payload)
	if err != nil {
		h.logger.Error("store import preview", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store import preview"})
		return
	}
	if err := logAdminAction(h.db, c, uid.String(), "data_import_previewed", "data_import_job", jobID.String(), map[string]any{"entity": request.Entity, "mode": request.Mode, "rows": len(rows), "errors": len(plan.Errors), "checksum": checksum}); err != nil {
		h.logger.Warn("audit import preview", zap.Error(err))
	}
	c.JSON(http.StatusCreated, gin.H{"job_id": jobID, "checksum": checksum, "entity": request.Entity, "mode": request.Mode, "source_name": request.SourceName, "row_count": len(rows), "expires_at": time.Now().UTC().Add(24 * time.Hour), "plan": plan})
}

func (h *DataHandler) ListImports(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, entity, source_format, import_mode, source_name, checksum, row_count, plan, status, result, error, applied_at, expires_at, created_at
		FROM data_import_jobs ORDER BY created_at DESC LIMIT 50
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load imports"})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var entity, format, mode, sourceName, checksum, status string
		var rowCount int
		var plan, result json.RawMessage
		var errorText *string
		var appliedAt *time.Time
		var expiresAt, createdAt time.Time
		if err := rows.Scan(&id, &entity, &format, &mode, &sourceName, &checksum, &rowCount, &plan, &status, &result, &errorText, &appliedAt, &expiresAt, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load imports"})
			return
		}
		items = append(items, gin.H{"id": id, "entity": entity, "format": format, "mode": mode, "source_name": sourceName, "checksum": checksum, "row_count": rowCount, "plan": plan, "status": status, "result": result, "error": errorText, "applied_at": appliedAt, "expires_at": expiresAt, "created_at": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"imports": items})
}

func (h *DataHandler) ApplyImport(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	jobID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid import job"})
		return
	}
	var request importApplyRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Checksum) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "preview checksum is required"})
		return
	}
	tx, err := h.db.Pool.BeginTx(c.Request.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start import"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var entity, mode, checksum, status string
	var planJSON, payloadJSON, resultJSON json.RawMessage
	var expiresAt time.Time
	err = tx.QueryRow(c.Request.Context(), `
		SELECT entity, import_mode, checksum, status, plan, payload, COALESCE(result, '{}'::jsonb), expires_at
		FROM data_import_jobs WHERE id = $1 FOR UPDATE
	`, jobID).Scan(&entity, &mode, &checksum, &status, &planJSON, &payloadJSON, &resultJSON, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "import job not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load import"})
		return
	}
	if status == "applied" {
		c.Header("Idempotent-Replay", "true")
		c.Data(http.StatusOK, "application/json", resultJSON)
		return
	}
	if status != "pending" || time.Now().After(expiresAt) {
		c.JSON(http.StatusConflict, gin.H{"error": "import preview is no longer active"})
		return
	}
	if request.Checksum != checksum {
		c.JSON(http.StatusConflict, gin.H{"error": "import checksum does not match the preview"})
		return
	}
	if entity == "teams" {
		if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtext('anvil:team-create'))`); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to lock team imports"})
			return
		}
	}
	var storedPlan importPlan
	var payload importPayload
	if json.Unmarshal(planJSON, &storedPlan) != nil || json.Unmarshal(payloadJSON, &payload) != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stored import preview is invalid"})
		return
	}
	if len(storedPlan.Errors) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "import preview contains validation errors"})
		return
	}
	currentPlan := h.planImport(c.Request.Context(), tx, entity, mode, payload.Rows, nil)
	if len(currentPlan.Errors) > 0 || currentPlan.StateChecksum != storedPlan.StateChecksum {
		c.JSON(http.StatusConflict, gin.H{"error": "data changed after preview; create a fresh preview", "plan": currentPlan})
		return
	}
	result, err := h.applyImportRows(c.Request.Context(), tx, entity, mode, payload.Rows, currentPlan)
	if err != nil {
		h.logger.Error("apply import", zap.String("job_id", jobID.String()), zap.Error(err))
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	resultJSON, _ = json.Marshal(result)
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE data_import_jobs SET status = 'applied', result = $2::jsonb, payload = '{"rows":[]}'::jsonb, applied_at = NOW() WHERE id = $1
	`, jobID, resultJSON); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finish import"})
		return
	}
	audit, _ := json.Marshal(map[string]any{"entity": entity, "mode": mode, "checksum": checksum, "result": result})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, 'data_import_applied', 'data_import_job', $2, $3::jsonb, $4, $5)
	`, uid, jobID, audit, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to audit import"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		if postgresErrorCode(err) == "40001" {
			c.JSON(http.StatusConflict, gin.H{"error": "data changed while importing; retry from a fresh preview"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit import"})
		return
	}
	c.Data(http.StatusOK, "application/json", resultJSON)
}

func parseImportContent(entity, format string, content []byte) ([]map[string]string, []importIssue) {
	if format == "csv" {
		return parseImportCSV(entity, content)
	}
	return parseImportJSON(entity, content)
}

func parseImportCSV(entity string, content []byte) ([]map[string]string, []importIssue) {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, []importIssue{{Row: 1, Message: "invalid CSV: " + err.Error()}}
	}
	if len(records) == 0 {
		return nil, []importIssue{{Row: 1, Message: "CSV is empty"}}
	}
	headers := make([]string, len(records[0]))
	seen := map[string]bool{}
	issues := []importIssue{}
	for index, header := range records[0] {
		header = normalizeImportHeader(header)
		headers[index] = header
		if header == "" || seen[header] {
			issues = append(issues, importIssue{Row: 1, Field: header, Message: "header is empty or duplicated"})
		}
		seen[header] = true
	}
	rows := make([]map[string]string, 0, len(records)-1)
	for index, record := range records[1:] {
		if len(record) != len(headers) {
			issues = append(issues, importIssue{Row: index + 2, Message: "column count does not match the header"})
			continue
		}
		row := make(map[string]string, len(headers))
		empty := true
		for column, value := range record {
			value = strings.TrimSpace(value)
			row[headers[column]] = value
			if value != "" {
				empty = false
			}
		}
		if !empty {
			rows = append(rows, row)
		}
	}
	return rows, issues
}

func parseImportJSON(entity string, content []byte) ([]map[string]string, []importIssue) {
	var records []map[string]any
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, []importIssue{{Row: 1, Message: "JSON must be an array of objects"}}
	}
	rows := make([]map[string]string, 0, len(records))
	issues := []importIssue{}
	for index, record := range records {
		row := make(map[string]string, len(record))
		for key, value := range record {
			key = normalizeImportHeader(key)
			switch typed := value.(type) {
			case nil:
				row[key] = ""
			case string:
				row[key] = strings.TrimSpace(typed)
			case bool, float64:
				row[key] = fmt.Sprint(typed)
			default:
				encoded, err := json.Marshal(typed)
				if err != nil {
					issues = append(issues, importIssue{Row: index + 1, Field: key, Message: "value cannot be encoded"})
					continue
				}
				row[key] = string(encoded)
			}
		}
		rows = append(rows, row)
	}
	return rows, issues
}

func normalizeImportHeader(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "_"))
}

func (h *DataHandler) planImport(ctx context.Context, query importQuery, entity, mode string, rows []map[string]string, initial []importIssue) importPlan {
	spec := importSpecs[entity]
	plan := importPlan{Errors: append([]importIssue{}, initial...), Rows: []importRowPlan{}}
	allowed := map[string]bool{}
	for _, header := range spec.Headers {
		allowed[header] = true
	}
	seenKeys := map[string]int{}
	for index, row := range rows {
		rowNumber := index + 2
		if entity != "categories" && entity != "challenges" && entity != "users" && entity != "teams" && entity != "team_members" {
			rowNumber = index + 1
		}
		for field := range row {
			if !allowed[field] {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Field: field, Message: "unknown column"})
			}
		}
		for _, field := range spec.Required {
			if strings.TrimSpace(row[field]) == "" {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Field: field, Message: "value is required"})
			}
		}
		key := strings.TrimSpace(row[spec.Key])
		if first, duplicate := seenKeys[strings.ToLower(key)]; duplicate && key != "" {
			plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Field: spec.Key, Message: fmt.Sprintf("duplicates row %d", first)})
		} else if key != "" {
			seenKeys[strings.ToLower(key)] = rowNumber
		}
		plan.Errors = append(plan.Errors, h.validateImportRow(ctx, query, entity, rowNumber, row)...)
		exists, err := importRowExists(ctx, query, entity, row)
		if err != nil {
			plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Message: "could not inspect current data"})
			continue
		}
		action := "create"
		if exists && mode == "merge" {
			action = "update"
		} else if exists {
			action = "skip"
		}
		plan.Rows = append(plan.Rows, importRowPlan{Row: rowNumber, Key: key, Action: action})
		switch action {
		case "create":
			plan.Create++
		case "update":
			plan.Update++
		case "skip":
			plan.Skip++
		}
		if entity == "challenges" && action == "update" {
			var activity int
			if err := query.QueryRow(ctx, `
				SELECT (SELECT COUNT(*)::int FROM solves s JOIN challenges c ON c.id = s.challenge_id WHERE c.slug = $1) +
				       (SELECT COUNT(*)::int FROM instances i JOIN challenges c ON c.id = i.challenge_id WHERE c.slug = $1)
			`, row["slug"]).Scan(&activity); err != nil {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Message: "could not inspect challenge activity"})
			} else if activity > 0 {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Field: "slug", Message: "challenge has solves or instances and cannot be overwritten"})
			}
		}
	}
	if entity == "teams" && plan.Create > 0 {
		var maximum, current int
		if err := query.QueryRow(ctx, `
			SELECT
				COALESCE((SELECT (value #>> '{}')::int FROM platform_settings WHERE key = 'participants.max_teams'), 0),
				(SELECT COUNT(*)::int FROM teams)
		`).Scan(&maximum, &current); err != nil {
			plan.Errors = append(plan.Errors, importIssue{Row: 0, Message: "could not inspect the event team limit"})
		} else if maximum > 0 && current+plan.Create > maximum {
			plan.Errors = append(plan.Errors, importIssue{Row: 0, Field: "name", Message: fmt.Sprintf("import would exceed the maximum of %d teams", maximum)})
		}
	}
	state, _ := json.Marshal(struct {
		Create int             `json:"create"`
		Update int             `json:"update"`
		Skip   int             `json:"skip"`
		Errors []importIssue   `json:"errors"`
		Rows   []importRowPlan `json:"rows"`
	}{plan.Create, plan.Update, plan.Skip, plan.Errors, plan.Rows})
	sum := sha256.Sum256(state)
	plan.StateChecksum = hex.EncodeToString(sum[:])
	return plan
}

func (h *DataHandler) validateImportRow(ctx context.Context, query importQuery, entity string, rowNumber int, row map[string]string) []importIssue {
	issues := []importIssue{}
	add := func(field, message string) {
		issues = append(issues, importIssue{Row: rowNumber, Field: field, Message: message})
	}
	switch entity {
	case "categories":
		if row["slug"] != "" && (!importSlug.MatchString(row["slug"]) || len(row["slug"]) > 100) {
			add("slug", "use lowercase letters, numbers, and internal hyphens")
		}
		if len([]rune(row["name"])) > 100 {
			add("name", "must be at most 100 characters")
		}
		if row["color"] != "" && !importColor.MatchString(row["color"]) {
			add("color", "must be a six-digit hex color")
		}
		if _, err := importInt(row["sort_order"], 0, -100000, 100000); err != nil {
			add("sort_order", err.Error())
		}
	case "users":
		if row["username"] != "" && !importUsername.MatchString(row["username"]) {
			add("username", "must be 3-50 letters, numbers, underscores, or hyphens")
		}
		if row["email"] != "" {
			address, err := mail.ParseAddress(row["email"])
			if err != nil || !strings.EqualFold(address.Address, row["email"]) {
				add("email", "must be a valid email address")
			} else {
				var owner string
				err := query.QueryRow(ctx, `SELECT username FROM users WHERE LOWER(email) = LOWER($1) AND username <> $2`, row["email"], row["username"]).Scan(&owner)
				if err == nil {
					add("email", "is already assigned to another username")
				} else if !errors.Is(err, pgx.ErrNoRows) {
					add("email", "could not validate uniqueness")
				}
			}
		}
		if role := importDefault(row["role"], "user"); role != "user" && role != "author" {
			add("role", "must be user or author")
		}
		if status := importDefault(row["status"], "active"); status != "active" && status != "suspended" && status != "banned" {
			add("status", "must be active, suspended, or banned")
		}
		if _, err := importBool(row["email_verified"], false); err != nil {
			add("email_verified", err.Error())
		}
		var existingRole string
		if err := query.QueryRow(ctx, `SELECT role::text FROM users WHERE username = $1`, row["username"]).Scan(&existingRole); err == nil && existingRole == "admin" {
			add("username", "administrator accounts cannot be changed by import")
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			add("username", "could not validate account role")
		}
	case "teams":
		if len([]rune(row["name"])) > 100 {
			add("name", "must be at most 100 characters")
		}
		if _, err := importInt(row["max_members"], 4, 1, 100); err != nil {
			add("max_members", err.Error())
		}
	case "team_members":
		var exists bool
		if err := query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, row["username"]).Scan(&exists); err != nil || !exists {
			add("username", "does not match an existing user")
		}
		if err := query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE name = $1)`, row["team_name"]).Scan(&exists); err != nil || !exists {
			add("team_name", "does not match an existing team")
		}
	case "challenges":
		if row["slug"] != "" && !importSlug.MatchString(row["slug"]) {
			add("slug", "use lowercase letters, numbers, and internal hyphens")
		}
		if len([]rune(row["name"])) > 200 {
			add("name", "must be at most 200 characters")
		}
		if difficulty := row["difficulty"]; difficulty != "easy" && difficulty != "medium" && difficulty != "hard" && difficulty != "insane" {
			add("difficulty", "must be easy, medium, hard, or insane")
		}
		if status := importDefault(row["status"], "draft"); status != "draft" && status != "published" && status != "archived" {
			add("status", "must be draft, published, or archived")
		}
		if resource := importDefault(row["resource_type"], "docker"); resource != "docker" && resource != "vm" {
			add("resource_type", "must be docker or vm")
		}
		delivery := challengeImportDelivery(row)
		if delivery != "docker" && delivery != "static" && delivery != "external" && delivery != "vm" {
			add("delivery_type", "must be docker, static, external, or vm")
		}
		if (importDefault(row["resource_type"], "docker") == "vm") != (delivery == "vm") {
			add("delivery_type", "must match the VM resource type")
		}
		if scoring := importDefault(row["scoring_mode"], "flag"); scoring != "flag" && scoring != "graded" {
			add("scoring_mode", "must be flag or graded")
		}
		if _, err := importInt(row["base_points"], 100, 0, 1000000); err != nil {
			add("base_points", err.Error())
		}
		if _, err := importBool(row["privesc"], false); err != nil {
			add("privesc", err.Error())
		}
		if row["release_date"] != "" {
			if _, err := time.Parse(time.RFC3339, row["release_date"]); err != nil {
				add("release_date", "must be an RFC3339 timestamp")
			}
		}
		if exposed := importDefault(row["exposed_ports"], "[]"); !json.Valid([]byte(exposed)) {
			add("exposed_ports", "must be valid JSON")
		}
		if row["category_slug"] != "" {
			var exists bool
			if err := query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE slug = $1)`, row["category_slug"]).Scan(&exists); err != nil || !exists {
				add("category_slug", "does not match an existing category")
			}
		}
	}
	return issues
}

func importRowExists(ctx context.Context, query importQuery, entity string, row map[string]string) (bool, error) {
	var exists bool
	var err error
	switch entity {
	case "categories":
		err = query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE slug = $1)`, row["slug"]).Scan(&exists)
	case "challenges":
		err = query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM challenges WHERE slug = $1)`, row["slug"]).Scan(&exists)
	case "users":
		err = query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, row["username"]).Scan(&exists)
	case "teams":
		err = query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE name = $1)`, row["name"]).Scan(&exists)
	case "team_members":
		err = query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1 AND team_id IS NOT NULL)`, row["username"]).Scan(&exists)
	default:
		return false, errors.New("unsupported entity")
	}
	return exists, err
}

func (h *DataHandler) applyImportRows(ctx context.Context, tx pgx.Tx, entity, mode string, rows []map[string]string, plan importPlan) (map[string]any, error) {
	applied := map[string]any{"entity": entity, "mode": mode, "created": 0, "updated": 0, "skipped": 0}
	created, updated, skipped := 0, 0, 0
	for index, rowPlan := range plan.Rows {
		if rowPlan.Action == "skip" {
			skipped++
			continue
		}
		row := rows[index]
		var err error
		if rowPlan.Action == "create" {
			err = applyImportCreate(ctx, tx, entity, row)
			created++
		} else {
			err = applyImportUpdate(ctx, tx, entity, row)
			updated++
		}
		if err != nil {
			return nil, fmt.Errorf("row %d (%s): %w", rowPlan.Row, rowPlan.Key, err)
		}
	}
	applied["created"], applied["updated"], applied["skipped"] = created, updated, skipped
	return applied, nil
}

func applyImportCreate(ctx context.Context, tx pgx.Tx, entity string, row map[string]string) error {
	switch entity {
	case "categories":
		sortOrder, _ := importInt(row["sort_order"], 0, -100000, 100000)
		_, err := tx.Exec(ctx, `INSERT INTO categories (slug, name, description, color, icon, sort_order) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6)`, row["slug"], row["name"], row["description"], row["color"], row["icon"], sortOrder)
		return err
	case "users":
		verified, _ := importBool(row["email_verified"], false)
		_, err := tx.Exec(ctx, `INSERT INTO users (username, email, display_name, role, status, email_verified) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4::user_role, $5::user_status, $6)`, row["username"], strings.ToLower(row["email"]), row["display_name"], importDefault(row["role"], "user"), importDefault(row["status"], "active"), verified)
		return err
	case "teams":
		maxMembers, _ := importInt(row["max_members"], 4, 1, 100)
		code, err := generateSecureToken(12)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO teams (name, join_code, max_members) VALUES ($1, $2, $3)`, row["name"], code, maxMembers)
		return err
	case "team_members":
		return assignImportedTeamMember(ctx, tx, row["username"], row["team_name"], false)
	case "challenges":
		return createChallengeImport(ctx, tx, row)
	default:
		return errors.New("unsupported entity")
	}
}

func applyImportUpdate(ctx context.Context, tx pgx.Tx, entity string, row map[string]string) error {
	switch entity {
	case "categories":
		sortOrder, _ := importInt(row["sort_order"], 0, -100000, 100000)
		_, err := tx.Exec(ctx, `UPDATE categories SET name = $2, description = NULLIF($3, ''), color = NULLIF($4, ''), icon = NULLIF($5, ''), sort_order = $6 WHERE slug = $1`, row["slug"], row["name"], row["description"], row["color"], row["icon"], sortOrder)
		return err
	case "users":
		verified, _ := importBool(row["email_verified"], false)
		_, err := tx.Exec(ctx, `UPDATE users SET email = NULLIF($2, ''), display_name = NULLIF($3, ''), role = $4::user_role, status = $5::user_status, email_verified = $6, updated_at = NOW() WHERE username = $1`, row["username"], strings.ToLower(row["email"]), row["display_name"], importDefault(row["role"], "user"), importDefault(row["status"], "active"), verified)
		return err
	case "teams":
		maxMembers, _ := importInt(row["max_members"], 4, 1, 100)
		var members int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM users u JOIN teams t ON t.id = u.team_id WHERE t.name = $1`, row["name"]).Scan(&members); err != nil {
			return err
		}
		if maxMembers < members {
			return fmt.Errorf("max_members cannot be below the current member count %d", members)
		}
		_, err := tx.Exec(ctx, `UPDATE teams SET max_members = $2, updated_at = NOW() WHERE name = $1`, row["name"], maxMembers)
		return err
	case "team_members":
		return assignImportedTeamMember(ctx, tx, row["username"], row["team_name"], true)
	case "challenges":
		return updateChallengeImport(ctx, tx, row)
	default:
		return errors.New("unsupported entity")
	}
}

func assignImportedTeamMember(ctx context.Context, tx pgx.Tx, username, teamName string, move bool) error {
	var teamID uuid.UUID
	var maxMembers *int
	if err := tx.QueryRow(ctx, `SELECT id, max_members FROM teams WHERE name = $1 FOR UPDATE`, teamName).Scan(&teamID, &maxMembers); err != nil {
		return err
	}
	var currentTeam *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT team_id FROM users WHERE username = $1 FOR UPDATE`, username).Scan(&currentTeam); err != nil {
		return err
	}
	if currentTeam != nil && *currentTeam == teamID {
		return nil
	}
	if currentTeam != nil && !move {
		return errors.New("user already belongs to another team")
	}
	if maxMembers != nil {
		var members int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM users WHERE team_id = $1`, teamID).Scan(&members); err != nil {
			return err
		}
		if members >= *maxMembers {
			return errors.New("target team is full")
		}
	}
	_, err := tx.Exec(ctx, `UPDATE users SET team_id = $2, updated_at = NOW() WHERE username = $1`, username, teamID)
	return err
}

func createChallengeImport(ctx context.Context, tx pgx.Tx, row map[string]string) error {
	points, _ := importInt(row["base_points"], 100, 0, 1000000)
	privesc, _ := importBool(row["privesc"], false)
	release, err := importTime(row["release_date"])
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO challenges
		(slug, name, description, sub_description, difficulty, category_id, status, author_name, resource_type, delivery_type,
		 container_image, container_tag, cpu_limit, memory_limit, exposed_ports, base_points, release_date, privesc, scoring_mode)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5::challenge_difficulty,
		 (SELECT id FROM categories WHERE slug = NULLIF($6, '')), $7::challenge_status, NULLIF($8, ''), $9::resource_type,
		 $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18, $19)
	`, row["slug"], row["name"], row["description"], row["sub_description"], row["difficulty"], row["category_slug"], importDefault(row["status"], "draft"), row["author_name"], importDefault(row["resource_type"], "docker"), challengeImportDelivery(row), row["container_image"], importDefault(row["container_tag"], "latest"), importDefault(row["cpu_limit"], "1"), importDefault(row["memory_limit"], "512m"), importDefault(row["exposed_ports"], "[]"), points, release, privesc, importDefault(row["scoring_mode"], "flag"))
	return err
}

func updateChallengeImport(ctx context.Context, tx pgx.Tx, row map[string]string) error {
	points, _ := importInt(row["base_points"], 100, 0, 1000000)
	privesc, _ := importBool(row["privesc"], false)
	release, err := importTime(row["release_date"])
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE challenges SET
		 name = $2, description = NULLIF($3, ''), sub_description = NULLIF($4, ''), difficulty = $5::challenge_difficulty,
		 category_id = (SELECT id FROM categories WHERE slug = NULLIF($6, '')), status = $7::challenge_status,
		 author_name = NULLIF($8, ''), resource_type = $9::resource_type, delivery_type = $10, container_image = $11, container_tag = $12,
		 cpu_limit = $13, memory_limit = $14, exposed_ports = $15::jsonb, base_points = $16,
		 release_date = $17, privesc = $18, scoring_mode = $19, updated_at = NOW()
		WHERE slug = $1
	`, row["slug"], row["name"], row["description"], row["sub_description"], row["difficulty"], row["category_slug"], importDefault(row["status"], "draft"), row["author_name"], importDefault(row["resource_type"], "docker"), challengeImportDelivery(row), row["container_image"], importDefault(row["container_tag"], "latest"), importDefault(row["cpu_limit"], "1"), importDefault(row["memory_limit"], "512m"), importDefault(row["exposed_ports"], "[]"), points, release, privesc, importDefault(row["scoring_mode"], "flag"))
	return err
}

func challengeImportDelivery(row map[string]string) string {
	if value := strings.ToLower(strings.TrimSpace(row["delivery_type"])); value != "" {
		return value
	}
	if importDefault(row["resource_type"], "docker") == "vm" {
		return "vm"
	}
	if strings.TrimSpace(row["container_image"]) != "" {
		return "docker"
	}
	return "static"
}

func importDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func importInt(value string, fallback, minimum, maximum int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("must be an integer from %d to %d", minimum, maximum)
	}
	return parsed, nil
}

func importBool(value string, fallback bool) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("must be true or false")
	}
	return parsed, nil
}

func importTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("must be an RFC3339 timestamp")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
