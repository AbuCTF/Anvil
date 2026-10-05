package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const maxImportBytes = 5 << 20
const maxImportRows = 5000
const maxImportRequestBytes = 7 << 20
const maxWorkbookUnzipBytes = 64 << 20
const maxWorkbookXMLBytes = 16 << 20

type importPreviewRequest struct {
	Entity       string            `json:"entity"`
	Format       string            `json:"format"`
	Mode         string            `json:"mode"`
	SourceName   string            `json:"source_name"`
	Content      string            `json:"content"`
	Provisioning string            `json:"provisioning"`
	Sheet        string            `json:"sheet"`
	ColumnMap    map[string]string `json:"column_map"`
}

type workbookInspectRequest struct {
	Entity  string `json:"entity"`
	Content string `json:"content"`
}

type importInspectRequest struct {
	Entity  string `json:"entity"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

type workbookHeader struct {
	Source         string `json:"source"`
	Normalized     string `json:"normalized"`
	SuggestedField string `json:"suggested_field"`
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

type importOptions struct {
	Provisioning string `json:"provisioning,omitempty"`
}

type participantCreateRequest struct {
	Username     string `json:"username"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Provisioning string `json:"provisioning"`
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
		Headers:  []string{"slug", "name", "description", "sub_description", "difficulty", "category_slug", "status", "author_name", "resource_type", "delivery_type", "container_image", "container_tag", "cpu_limit", "memory_limit", "exposed_ports", "base_points", "score_type", "score_minimum", "score_decay", "release_date", "privesc", "scoring_mode"},
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

var importHeaderAliases = map[string]map[string]string{
	"categories": {
		"category": "name", "category_name": "name", "category_slug": "slug", "order": "sort_order", "colour": "color",
	},
	"challenges": {
		"title": "name", "challenge_name": "name", "challenge_slug": "slug", "category": "category_slug", "author": "author_name",
		"points": "base_points", "score": "base_points", "value": "base_points", "image": "container_image", "docker_image": "container_image",
		"repository": "container_image", "tag": "container_tag", "ports": "exposed_ports", "release": "release_date", "type": "delivery_type",
		"scoring": "score_type", "minimum": "score_minimum", "decay": "score_decay", "evaluation": "scoring_mode",
	},
	"users": {
		"user_name": "username", "user_id": "username", "userid": "username", "login": "username", "login_id": "username",
		"participant_id": "username", "employee_id": "username", "roll_number": "username", "roll_no": "username", "registration_number": "username", "registration_no": "username",
		"email_address": "email", "email_id": "email", "e_mail": "email", "mail": "email",
		"name": "display_name", "full_name": "display_name", "participant_name": "display_name", "student_name": "display_name", "employee_name": "display_name",
		"user_role": "role", "account_role": "role", "account_status": "status", "verified": "email_verified", "is_verified": "email_verified",
	},
	"teams": {
		"team": "name", "team_name": "name", "group": "name", "group_name": "name", "team_size": "max_members", "maximum_members": "max_members",
	},
	"team_members": {
		"user": "username", "user_name": "username", "participant": "username", "participant_username": "username", "member": "username",
		"team": "team_name", "teamname": "team_name", "group": "team_name", "group_name": "team_name",
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
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "csv")))
	switch format {
	case "csv":
		data, err := encodeCollectionCSV(exportCollection{Headers: spec.Headers, Rows: []map[string]any{}})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="anvil-%s-template.csv"`, entity))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
	case "json":
		row := make(map[string]string, len(spec.Headers))
		for _, header := range spec.Headers {
			row[header] = ""
		}
		data, err := json.MarshalIndent([]map[string]string{row}, "", "  ")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="anvil-%s-template.json"`, entity))
		c.Data(http.StatusOK, "application/json", append(data, '\n'))
	case "xlsx":
		workbook := excelize.NewFile()
		defer workbook.Close()
		name := "Import"
		workbook.SetSheetName("Sheet1", name)
		for index, header := range spec.Headers {
			cell, _ := excelize.CoordinatesToCellName(index+1, 1)
			if err := workbook.SetCellValue(name, cell, header); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
				return
			}
		}
		buffer, err := workbook.WriteToBuffer()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="anvil-%s-template.xlsx"`, entity))
		c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buffer.Bytes())
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv, json, or xlsx"})
	}
}

func (h *DataHandler) CreateParticipant(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var request participantCreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid participant request"})
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Role = strings.ToLower(strings.TrimSpace(request.Role))
	request.Provisioning = strings.ToLower(strings.TrimSpace(request.Provisioning))
	if request.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	if request.Role == "" {
		request.Role = "user"
	}
	if request.Role != "user" && request.Role != "author" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be user or author"})
		return
	}
	if request.Provisioning == "" {
		request.Provisioning = "activation_email"
	}
	if request.Provisioning != "sso_only" && request.Provisioning != "activation_email" && request.Provisioning != "generated_credentials" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provisioning must be sso_only, activation_email, or generated_credentials"})
		return
	}
	if request.Provisioning != "sso_only" && (h.mailSvc == nil || !h.mailSvc.Ready(c.Request.Context())) {
		c.JSON(http.StatusConflict, gin.H{"error": "configure and successfully test an active mail provider before emailing participant access"})
		return
	}
	rows := []map[string]string{{
		"username": request.Username, "email": request.Email, "display_name": request.DisplayName,
		"role": request.Role, "status": "active", "email_verified": "false",
	}}
	issues := prepareGeneratedCredentialUsernames(c.Request.Context(), h.db.Pool, rows)
	plan := h.planImport(c.Request.Context(), h.db.Pool, "users", "create", rows, issues)
	if request.Provisioning != "sso_only" {
		if _, _, err := importEventIdentity(c.Request.Context(), h.db.Pool); err != nil {
			plan.Errors = append(plan.Errors, importIssue{Field: "event.public_url", Message: err.Error()})
		}
	}
	if len(plan.Errors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": plan.Errors[0].Message, "field": plan.Errors[0].Field, "issues": plan.Errors})
		return
	}
	if len(plan.Rows) != 1 || plan.Rows[0].Action != "create" {
		c.JSON(http.StatusConflict, gin.H{"error": "a participant with this username already exists"})
		return
	}
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create participant"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	if err := applyImportCreate(c.Request.Context(), tx, "users", rows[0]); err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "the username or email is already in use"})
		} else {
			h.logger.Error("create participant", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create participant"})
		}
		return
	}
	queued := 0
	if request.Provisioning == "activation_email" {
		queued, err = h.provisionImportedUsers(c.Request.Context(), tx, rows, plan, uid)
	} else if request.Provisioning == "generated_credentials" {
		queued, err = h.provisionImportedCredentials(c.Request.Context(), tx, rows, plan, uid)
	}
	if err != nil {
		h.logger.Error("provision participant", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "participant access could not be prepared; no account was created"})
		return
	}
	var userID uuid.UUID
	if err := tx.QueryRow(c.Request.Context(), `SELECT id FROM users WHERE username = $1`, rows[0]["username"]).Scan(&userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create participant"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create participant"})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "participant_created", "user", userID.String(), map[string]interface{}{
		"username": rows[0]["username"], "role": request.Role, "provisioning": request.Provisioning,
	})
	c.JSON(http.StatusCreated, gin.H{"id": userID, "username": rows[0]["username"], "provisioning": request.Provisioning, "emails_queued": queued})
}

func (h *DataHandler) InspectImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportRequestBytes)
	var request importInspectRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid import inspection request"})
		return
	}
	request.Entity = strings.ToLower(strings.TrimSpace(request.Entity))
	request.Format = strings.ToLower(strings.TrimSpace(request.Format))
	spec, ok := importSpecs[request.Entity]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported import entity"})
		return
	}
	if request.Format != "csv" && request.Format != "json" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv or json"})
		return
	}
	content := []byte(request.Content)
	if len(content) == 0 || len(content) > maxImportBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "import content must be between 1 byte and 5 MB"})
		return
	}
	headers := []string{}
	rowCount := 0
	if request.Format == "csv" {
		reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})))
		reader.FieldsPerRecord = -1
		records, err := reader.ReadAll()
		if err != nil || len(records) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the file is not readable CSV"})
			return
		}
		headers = records[0]
		rowCount = len(records) - 1
	} else {
		var document any
		if err := json.Unmarshal(content, &document); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the file is not readable JSON"})
			return
		}
		records, err := importJSONRecords(request.Entity, document)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		rowCount = len(records)
		seen := map[string]bool{}
		for _, record := range records {
			keys := make([]string, 0, len(record))
			for key := range record {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if !seen[key] {
					seen[key] = true
					headers = append(headers, key)
				}
			}
		}
	}
	if rowCount > maxImportRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": "imports are limited to 5000 rows"})
		return
	}
	inspected, recognized, missing := inspectImportHeaders(request.Entity, headers, spec)
	label := strings.ToUpper(request.Format)
	c.JSON(http.StatusOK, gin.H{
		"sheets":            []gin.H{{"name": label, "rows": rowCount, "recognized_headers": recognized, "missing_required_headers": missing, "headers": inspected}},
		"recommended_sheet": label, "fields": spec.Headers, "required_fields": spec.Required,
	})
}

func inspectImportHeaders(entity string, headers []string, spec importSpec) ([]workbookHeader, int, []string) {
	result := make([]workbookHeader, 0, len(headers))
	recognized := 0
	headerSet := map[string]bool{}
	for _, header := range headers {
		normalized := normalizeImportHeader(header)
		suggested := canonicalImportHeader(entity, normalized)
		result = append(result, workbookHeader{Source: strings.TrimSpace(header), Normalized: normalized, SuggestedField: suggested})
		if suggested != "" {
			headerSet[suggested] = true
			recognized++
		}
	}
	missing := []string{}
	for _, required := range spec.Required {
		if !headerSet[required] {
			missing = append(missing, required)
		}
	}
	return result, recognized, missing
}

func (h *DataHandler) InspectWorkbook(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportRequestBytes)
	var request workbookInspectRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workbook request"})
		return
	}
	request.Entity = strings.ToLower(strings.TrimSpace(request.Entity))
	spec, ok := importSpecs[request.Entity]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported import entity"})
		return
	}
	content, err := decodeImportWorkbook(request.Content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	workbook, err := openImportWorkbook(content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the file is not a readable .xlsx workbook"})
		return
	}
	defer workbook.Close()
	sheets := []gin.H{}
	recommended := ""
	bestScore := -1
	for _, name := range workbook.GetSheetList() {
		rows, err := workbook.Rows(name)
		if err != nil {
			continue
		}
		headers := []workbookHeader{}
		rowCount := 0
		for rows.Next() {
			columns, rowErr := rows.Columns()
			if rowErr != nil {
				err = rowErr
				break
			}
			rowCount++
			if rowCount == 1 {
				for _, header := range columns {
					normalized := normalizeImportHeader(header)
					headers = append(headers, workbookHeader{Source: strings.TrimSpace(header), Normalized: normalized, SuggestedField: canonicalImportHeader(request.Entity, normalized)})
				}
			}
			if rowCount > maxImportRows+1 {
				break
			}
		}
		_ = rows.Close()
		if err != nil {
			continue
		}
		recognized := 0
		headerSet := map[string]bool{}
		for _, header := range headers {
			if header.SuggestedField != "" {
				headerSet[header.SuggestedField] = true
				recognized++
			}
		}
		missing := []string{}
		for _, required := range spec.Required {
			if !headerSet[required] {
				missing = append(missing, required)
			}
		}
		sheets = append(sheets, gin.H{"name": name, "rows": max(rowCount-1, 0), "recognized_headers": recognized, "missing_required_headers": missing, "headers": headers})
		score := recognized*10 - len(missing)*100
		if len(headers) > 0 && score > bestScore {
			bestScore = score
			recommended = name
		}
	}
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the workbook has no readable sheets"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sheets": sheets, "recommended_sheet": recommended, "fields": spec.Headers, "required_fields": spec.Required})
}

func (h *DataHandler) PreviewImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportRequestBytes)
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
	request.Provisioning = strings.ToLower(strings.TrimSpace(request.Provisioning))
	request.Sheet = strings.TrimSpace(request.Sheet)
	spec, ok := importSpecs[request.Entity]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported import entity"})
		return
	}
	if len(request.ColumnMap) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "column mapping contains too many fields"})
		return
	}
	allowedColumns := map[string]bool{}
	for _, header := range spec.Headers {
		allowedColumns[header] = true
	}
	normalizedColumnMap := make(map[string]string, len(request.ColumnMap))
	for source, target := range request.ColumnMap {
		normalizedSource := normalizeImportHeader(source)
		if normalizedSource == "" || len(normalizedSource) > 100 || (target != "" && !allowedColumns[target]) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "column mapping contains an unsupported field"})
			return
		}
		if _, duplicate := normalizedColumnMap[normalizedSource]; duplicate {
			c.JSON(http.StatusBadRequest, gin.H{"error": "column mapping contains duplicate source fields"})
			return
		}
		normalizedColumnMap[normalizedSource] = target
	}
	request.ColumnMap = normalizedColumnMap
	if request.Format != "csv" && request.Format != "json" && request.Format != "xlsx" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv, json, or xlsx"})
		return
	}
	if request.Mode != "create" && request.Mode != "merge" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be create or merge"})
		return
	}
	if request.Entity == "users" {
		if request.Provisioning == "" {
			request.Provisioning = "sso_only"
		}
		if request.Provisioning != "sso_only" && request.Provisioning != "activation_email" && request.Provisioning != "generated_credentials" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provisioning must be sso_only, activation_email, or generated_credentials"})
			return
		}
		if request.Provisioning != "sso_only" && (h.mailSvc == nil || !h.mailSvc.Ready(c.Request.Context())) {
			c.JSON(http.StatusConflict, gin.H{"error": "configure and successfully test an active mail provider before emailing participant access"})
			return
		}
	} else if request.Provisioning != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provisioning is only available for user imports"})
		return
	}
	if request.SourceName == "" || len(request.SourceName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_name is required"})
		return
	}
	if len(request.Content) == 0 || len(request.Content) > maxImportRequestBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "import content must be between 1 byte and 5 MB"})
		return
	}
	content := []byte(request.Content)
	if request.Format == "xlsx" {
		if request.Sheet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "select a workbook sheet"})
			return
		}
		decoded, err := decodeImportWorkbook(request.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		content = decoded
	} else if len(content) > maxImportBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "import content must be at most 5 MB"})
		return
	}
	rows, issues := parseImportContent(request.Entity, request.Format, content, request.Sheet, request.ColumnMap)
	if len(rows) > maxImportRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": "imports are limited to 5000 rows"})
		return
	}
	if request.Entity == "users" && request.Provisioning != "sso_only" {
		issues = append(issues, prepareGeneratedCredentialUsernames(c.Request.Context(), h.db.Pool, rows)...)
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
	if request.Entity == "users" && request.Provisioning != "sso_only" {
		for index, rowPlan := range plan.Rows {
			if rowPlan.Action == "create" && strings.TrimSpace(rows[index]["email"]) == "" {
				plan.Errors = append(plan.Errors, importIssue{Row: rowPlan.Row, Field: "email", Message: "is required when email provisioning is enabled"})
			}
		}
		if _, _, err := importEventIdentity(c.Request.Context(), h.db.Pool); err != nil {
			plan.Errors = append(plan.Errors, importIssue{Row: 0, Field: "event.public_url", Message: err.Error()})
		}
		refreshImportPlanChecksum(&plan)
	}
	payload, _ := json.Marshal(importPayload{Rows: rows})
	options, _ := json.Marshal(importOptions{Provisioning: request.Provisioning})
	planJSON, _ := json.Marshal(plan)
	columnMapJSON, _ := json.Marshal(request.ColumnMap)
	checksumInput := append(append([]byte{}, content...), []byte("\x00"+request.Sheet+"\x00")...)
	checksumInput = append(checksumInput, columnMapJSON...)
	sum := sha256.Sum256(checksumInput)
	checksum := hex.EncodeToString(sum[:])
	jobID := uuid.New()
	jobStatus := "pending"
	var jobError *string
	if len(plan.Errors) > 0 {
		jobStatus = "failed"
		message := fmt.Sprintf("validation found %d issue(s); fix the source file and preview it again", len(plan.Errors))
		jobError = &message
		payload = []byte(`{"rows":[]}`)
	}
	_, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO data_import_jobs
		(id, created_by, entity, source_format, import_mode, source_name, checksum, row_count, plan, payload, options, status, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb, $12, $13)
	`, jobID, uid, request.Entity, request.Format, request.Mode, request.SourceName, checksum, len(rows), planJSON, payload, options, jobStatus, jobError)
	if err != nil {
		h.logger.Error("store import preview", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store import preview"})
		return
	}
	if err := logAdminAction(h.db, c, uid.String(), "data_import_previewed", "data_import_job", jobID.String(), map[string]any{"entity": request.Entity, "mode": request.Mode, "rows": len(rows), "errors": len(plan.Errors), "checksum": checksum}); err != nil {
		h.logger.Warn("audit import preview", zap.Error(err))
	}
	c.JSON(http.StatusCreated, gin.H{"job_id": jobID, "checksum": checksum, "entity": request.Entity, "mode": request.Mode, "source_name": request.SourceName, "row_count": len(rows), "expires_at": time.Now().UTC().Add(24 * time.Hour), "provisioning": request.Provisioning, "plan": plan})
}

func (h *DataHandler) ListImports(c *gin.Context) {
	if _, err := h.db.Pool.Exec(c.Request.Context(), `UPDATE data_import_jobs SET status = 'expired', payload = '{"rows":[]}'::jsonb WHERE status = 'pending' AND expires_at <= NOW()`); err != nil {
		h.logger.Error("expire import previews", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load imports"})
		return
	}
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, entity, source_format, import_mode, source_name, checksum, row_count, plan, options, status, result, error, applied_at, expires_at, created_at
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
		var plan, options, result json.RawMessage
		var errorText *string
		var appliedAt *time.Time
		var expiresAt, createdAt time.Time
		if err := rows.Scan(&id, &entity, &format, &mode, &sourceName, &checksum, &rowCount, &plan, &options, &status, &result, &errorText, &appliedAt, &expiresAt, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load imports"})
			return
		}
		items = append(items, gin.H{"id": id, "entity": entity, "format": format, "mode": mode, "source_name": sourceName, "checksum": checksum, "row_count": rowCount, "plan": plan, "options": options, "status": status, "result": result, "error": errorText, "applied_at": appliedAt, "expires_at": expiresAt, "created_at": createdAt})
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
	var planJSON, payloadJSON, optionsJSON, resultJSON json.RawMessage
	var expiresAt time.Time
	err = tx.QueryRow(c.Request.Context(), `
		SELECT entity, import_mode, checksum, status, plan, payload, options, COALESCE(result, '{}'::jsonb), expires_at
		FROM data_import_jobs WHERE id = $1 FOR UPDATE
	`, jobID).Scan(&entity, &mode, &checksum, &status, &planJSON, &payloadJSON, &optionsJSON, &resultJSON, &expiresAt)
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
	var options importOptions
	if json.Unmarshal(planJSON, &storedPlan) != nil || json.Unmarshal(payloadJSON, &payload) != nil || json.Unmarshal(optionsJSON, &options) != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stored import preview is invalid"})
		return
	}
	if len(storedPlan.Errors) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "import preview contains validation errors"})
		return
	}
	currentPlan := h.planImport(c.Request.Context(), tx, entity, mode, payload.Rows, nil)
	if len(currentPlan.Errors) > 0 || currentPlan.StateChecksum != storedPlan.StateChecksum {
		_ = tx.Rollback(c.Request.Context())
		h.recordImportFailure(c.Request.Context(), jobID, "data changed after preview; create a fresh preview")
		c.JSON(http.StatusConflict, gin.H{"error": "data changed after preview; create a fresh preview", "plan": currentPlan})
		return
	}
	result, err := h.applyImportRows(c.Request.Context(), tx, entity, mode, payload.Rows, currentPlan)
	if err != nil {
		h.logger.Error("apply import", zap.String("job_id", jobID.String()), zap.Error(err))
		_ = tx.Rollback(c.Request.Context())
		h.recordImportFailure(c.Request.Context(), jobID, err.Error())
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if entity == "users" && options.Provisioning == "activation_email" {
		queued, provisionErr := h.provisionImportedUsers(c.Request.Context(), tx, payload.Rows, currentPlan, uid)
		if provisionErr != nil {
			h.logger.Error("provision imported users", zap.String("job_id", jobID.String()), zap.Error(provisionErr))
			_ = tx.Rollback(c.Request.Context())
			h.recordImportFailure(c.Request.Context(), jobID, provisionErr.Error())
			c.JSON(http.StatusConflict, gin.H{"error": provisionErr.Error()})
			return
		}
		result["activation_emails_queued"] = queued
	}
	if entity == "users" && options.Provisioning == "generated_credentials" {
		queued, provisionErr := h.provisionImportedCredentials(c.Request.Context(), tx, payload.Rows, currentPlan, uid)
		if provisionErr != nil {
			h.logger.Error("provision imported credentials", zap.String("job_id", jobID.String()), zap.Error(provisionErr))
			_ = tx.Rollback(c.Request.Context())
			h.recordImportFailure(c.Request.Context(), jobID, provisionErr.Error())
			c.JSON(http.StatusConflict, gin.H{"error": provisionErr.Error()})
			return
		}
		result["credential_emails_queued"] = queued
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

func (h *DataHandler) recordImportFailure(ctx context.Context, jobID uuid.UUID, message string) {
	if len(message) > 2000 {
		message = message[:2000]
	}
	if _, err := h.db.Pool.Exec(ctx, `
		UPDATE data_import_jobs SET status = 'failed', error = $2, payload = '{"rows":[]}'::jsonb
		WHERE id = $1 AND status = 'pending'
	`, jobID, message); err != nil {
		h.logger.Warn("record import failure", zap.String("job_id", jobID.String()), zap.Error(err))
	}
}

func parseImportContent(entity, format string, content []byte, sheet string, columnMap map[string]string) ([]map[string]string, []importIssue) {
	if format == "csv" {
		return parseImportCSV(entity, content, columnMap)
	}
	if format == "xlsx" {
		return parseImportWorkbookForEntity(entity, content, sheet, columnMap)
	}
	return parseImportJSON(entity, content, columnMap)
}

func parseImportCSV(entity string, content []byte, mappings ...map[string]string) ([]map[string]string, []importIssue) {
	var columnMap map[string]string
	if len(mappings) > 0 {
		columnMap = mappings[0]
	}
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
	return parseImportTable(entity, records, true, columnMap)
}

func parseImportWorkbook(content []byte, sheet string, mappings ...map[string]string) ([]map[string]string, []importIssue) {
	columnMap := map[string]string(nil)
	if len(mappings) > 0 {
		columnMap = mappings[0]
	}
	return parseImportWorkbookForEntity("", content, sheet, columnMap)
}

func parseImportWorkbookForEntity(entity string, content []byte, sheet string, columnMap map[string]string) ([]map[string]string, []importIssue) {
	workbook, err := openImportWorkbook(content)
	if err != nil {
		return nil, []importIssue{{Row: 1, Message: "the file is not a readable .xlsx workbook"}}
	}
	defer workbook.Close()
	found := false
	for _, candidate := range workbook.GetSheetList() {
		if candidate == sheet {
			found = true
			break
		}
	}
	if !found {
		return nil, []importIssue{{Row: 1, Field: "sheet", Message: "the selected sheet is not present in the workbook"}}
	}
	iterator, err := workbook.Rows(sheet)
	if err != nil {
		return nil, []importIssue{{Row: 1, Field: "sheet", Message: "the selected sheet could not be read"}}
	}
	defer iterator.Close()
	records := [][]string{}
	for iterator.Next() {
		row, err := iterator.Columns()
		if err != nil {
			return nil, []importIssue{{Row: len(records) + 1, Message: "the workbook row could not be read"}}
		}
		records = append(records, row)
		if len(records) > maxImportRows+1 {
			return nil, []importIssue{{Row: maxImportRows + 2, Message: "imports are limited to 5000 data rows"}}
		}
	}
	if err := iterator.Error(); err != nil {
		return nil, []importIssue{{Row: len(records) + 1, Message: "the workbook sheet could not be read"}}
	}
	if len(records) == 0 {
		return nil, []importIssue{{Row: 1, Message: "the selected sheet is empty"}}
	}
	return parseImportTable(entity, records, false, columnMap)
}

func parseImportTable(entity string, records [][]string, strictColumns bool, columnMap map[string]string) ([]map[string]string, []importIssue) {
	headers := make([]string, len(records[0]))
	seen := map[string]bool{}
	issues := []importIssue{}
	for index, header := range records[0] {
		source := normalizeImportHeader(header)
		if source == "" {
			issues = append(issues, importIssue{Row: 1, Message: "header is empty"})
			continue
		}
		target, mapped := columnMap[source]
		if !mapped {
			target = canonicalImportHeader(entity, source)
			if target == "" {
				target = source
			}
		}
		headers[index] = target
		if target != "" && seen[target] {
			issues = append(issues, importIssue{Row: 1, Field: target, Message: "multiple source columns map to this field"})
		}
		if target != "" {
			seen[target] = true
		}
	}
	rows := make([]map[string]string, 0, len(records)-1)
	for index, record := range records[1:] {
		if strictColumns && len(record) != len(headers) {
			issues = append(issues, importIssue{Row: index + 2, Message: "column count does not match the header"})
			continue
		}
		if len(record) > len(headers) {
			hasExtraValue := false
			for _, value := range record[len(headers):] {
				if strings.TrimSpace(value) != "" {
					hasExtraValue = true
					break
				}
			}
			if hasExtraValue {
				issues = append(issues, importIssue{Row: index + 2, Message: "row has values beyond the last header"})
				continue
			}
			record = record[:len(headers)]
		}
		row := make(map[string]string, len(headers))
		empty := true
		for column, header := range headers {
			if header == "" {
				continue
			}
			value := ""
			if column < len(record) {
				value = record[column]
			}
			value = strings.TrimSpace(value)
			row[header] = value
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

func decodeImportWorkbook(encoded string) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > maxImportRequestBytes {
		return nil, errors.New("workbook must be between 1 byte and 5 MB")
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("workbook content is not valid base64")
	}
	if len(content) == 0 || len(content) > maxImportBytes {
		return nil, errors.New("workbook must be at most 5 MB")
	}
	return content, nil
}

func openImportWorkbook(content []byte) (*excelize.File, error) {
	return excelize.OpenReader(bytes.NewReader(content), excelize.Options{UnzipSizeLimit: maxWorkbookUnzipBytes, UnzipXMLSizeLimit: maxWorkbookXMLBytes})
}

func parseImportJSON(entity string, content []byte, mappings ...map[string]string) ([]map[string]string, []importIssue) {
	var columnMap map[string]string
	if len(mappings) > 0 {
		columnMap = mappings[0]
	}
	var document any
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, []importIssue{{Row: 1, Message: "invalid JSON: " + err.Error()}}
	}
	records, err := importJSONRecords(entity, document)
	if err != nil {
		return nil, []importIssue{{Row: 1, Message: err.Error()}}
	}
	rows := make([]map[string]string, 0, len(records))
	issues := []importIssue{}
	for index, record := range records {
		row := make(map[string]string, len(record))
		for key, value := range record {
			normalized := normalizeImportHeader(key)
			target, mapped := columnMap[normalized]
			if !mapped {
				target = canonicalImportHeader(entity, normalized)
				if target == "" {
					target = normalized
				}
			}
			if target == "" {
				continue
			}
			if _, duplicate := row[target]; duplicate {
				issues = append(issues, importIssue{Row: index + 1, Field: target, Message: "multiple source fields map to this field"})
				continue
			}
			switch typed := value.(type) {
			case nil:
				row[target] = ""
			case string:
				row[target] = strings.TrimSpace(typed)
			case bool, float64:
				row[target] = fmt.Sprint(typed)
			default:
				encoded, err := json.Marshal(typed)
				if err != nil {
					issues = append(issues, importIssue{Row: index + 1, Field: target, Message: "value cannot be encoded"})
					continue
				}
				row[target] = string(encoded)
			}
		}
		rows = append(rows, row)
	}
	return rows, issues
}

func importJSONRecords(entity string, document any) ([]map[string]any, error) {
	value := document
	if object, ok := value.(map[string]any); ok {
		if data, exists := object["data"]; exists {
			value = data
		}
		if data, ok := value.(map[string]any); ok {
			if selected, exists := data[entity]; exists {
				value = selected
			}
		}
		if collection, ok := value.(map[string]any); ok {
			if rows, exists := collection["rows"]; exists {
				value = rows
			}
		}
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("JSON must be an array, an object with rows, or an Anvil export containing data.%s", entity)
	}
	records := make([]map[string]any, 0, len(items))
	for index, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("JSON row %d must be an object", index+1)
		}
		records = append(records, record)
	}
	return records, nil
}

func normalizeImportHeader(value string) string {
	var normalized strings.Builder
	separator := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			if separator && normalized.Len() > 0 {
				normalized.WriteByte('_')
			}
			normalized.WriteRune(character)
			separator = false
		} else {
			separator = true
		}
	}
	return strings.Trim(normalized.String(), "_")
}

func canonicalImportHeader(entity, normalized string) string {
	if normalized == "" {
		return ""
	}
	spec, ok := importSpecs[entity]
	if !ok {
		return normalized
	}
	for _, header := range spec.Headers {
		if header == normalized {
			return header
		}
	}
	return importHeaderAliases[entity][normalized]
}

func prepareGeneratedCredentialUsernames(ctx context.Context, query importQuery, rows []map[string]string) []importIssue {
	issues := []importIssue{}
	reserved := map[string]bool{}
	seenEmails := map[string]int{}
	for _, row := range rows {
		if username := strings.TrimSpace(row["username"]); username != "" {
			reserved[strings.ToLower(username)] = true
		}
	}
	for index, row := range rows {
		rowNumber := index + 2
		email := strings.ToLower(strings.TrimSpace(row["email"]))
		row["email"] = email
		if email != "" {
			if first, exists := seenEmails[email]; exists {
				issues = append(issues, importIssue{Row: rowNumber, Field: "email", Message: fmt.Sprintf("duplicates row %d", first)})
			} else {
				seenEmails[email] = rowNumber
			}
		}
		if strings.TrimSpace(row["username"]) != "" {
			continue
		}
		if email != "" {
			var existing string
			err := query.QueryRow(ctx, `SELECT username FROM users WHERE LOWER(email) = LOWER($1)`, email).Scan(&existing)
			if err == nil {
				row["username"] = existing
				reserved[strings.ToLower(existing)] = true
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				issues = append(issues, importIssue{Row: rowNumber, Field: "username", Message: "could not derive a unique username"})
				continue
			}
		}
		base := generatedUsernameBase(email, row["display_name"])
		issueCount := len(issues)
		for sequence := 1; sequence <= maxImportRows+1; sequence++ {
			candidate := base
			if sequence > 1 {
				suffix := "_" + strconv.Itoa(sequence)
				candidate = strings.TrimRight(base[:min(len(base), 50-len(suffix))], "_") + suffix
			}
			if reserved[strings.ToLower(candidate)] {
				continue
			}
			var exists bool
			if err := query.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username) = LOWER($1))`, candidate).Scan(&exists); err != nil {
				issues = append(issues, importIssue{Row: rowNumber, Field: "username", Message: "could not derive a unique username"})
				break
			}
			if exists {
				continue
			}
			row["username"] = candidate
			reserved[strings.ToLower(candidate)] = true
			break
		}
		if row["username"] == "" && len(issues) == issueCount {
			issues = append(issues, importIssue{Row: rowNumber, Field: "username", Message: "could not derive a unique username"})
		}
	}
	return issues
}

func generatedUsernameBase(email, displayName string) string {
	value := displayName
	if separator := strings.Index(email, "@"); separator > 0 {
		value = email[:separator]
	}
	var output strings.Builder
	separatorPending := false
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			if separatorPending && output.Len() > 0 {
				output.WriteByte('_')
			}
			output.WriteRune(character)
			separatorPending = false
		} else {
			separatorPending = true
		}
	}
	base := strings.Trim(output.String(), "_")
	if len(base) < 3 {
		base = "participant_" + base
	}
	base = strings.TrimRight(base[:min(len(base), 50)], "_")
	if len(base) < 3 {
		return "participant"
	}
	return base
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
				       (SELECT COUNT(*)::int FROM submissions s JOIN challenges c ON c.id = s.challenge_id WHERE c.slug = $1) +
				       (SELECT COUNT(*)::int FROM graded_evaluations e JOIN challenges c ON c.id = e.challenge_id WHERE c.slug = $1) +
				       (SELECT COUNT(*)::int FROM instances i JOIN challenges c ON c.id = i.challenge_id WHERE c.slug = $1)
			`, row["slug"]).Scan(&activity); err != nil {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Message: "could not inspect challenge activity"})
			} else if activity > 0 {
				plan.Errors = append(plan.Errors, importIssue{Row: rowNumber, Field: "slug", Message: "challenge has participant or runtime activity and cannot be overwritten; use a new slug"})
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
	refreshImportPlanChecksum(&plan)
	return plan
}

func refreshImportPlanChecksum(plan *importPlan) {
	state, _ := json.Marshal(struct {
		Create int             `json:"create"`
		Update int             `json:"update"`
		Skip   int             `json:"skip"`
		Errors []importIssue   `json:"errors"`
		Rows   []importRowPlan `json:"rows"`
	}{plan.Create, plan.Update, plan.Skip, plan.Errors, plan.Rows})
	sum := sha256.Sum256(state)
	plan.StateChecksum = hex.EncodeToString(sum[:])
}

func importEventIdentity(ctx context.Context, query importQuery) (string, string, error) {
	var publicURL, eventName string
	err := query.QueryRow(ctx, `
		SELECT
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'event.public_url'), ''),
			COALESCE(MAX(value #>> '{}') FILTER (WHERE key = 'platform_name'), 'Anvil')
		FROM platform_settings WHERE key IN ('event.public_url', 'platform_name')
	`).Scan(&publicURL, &eventName)
	if err != nil {
		return "", "", errors.New("could not load the event link settings")
	}
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", "", errors.New("set a clean HTTPS event URL before emailing account activations")
	}
	if strings.TrimSpace(eventName) == "" {
		eventName = "Anvil"
	}
	return publicURL, eventName, nil
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
		evaluation := importDefault(row["scoring_mode"], "flag")
		if evaluation != "flag" && evaluation != "graded" {
			add("scoring_mode", "must be flag or graded")
		}
		points, pointsErr := importInt(row["base_points"], 100, 0, 1000000)
		if pointsErr != nil {
			add("base_points", pointsErr.Error())
		}
		scoreType := importDefault(row["score_type"], "static")
		if scoreType != "static" && scoreType != "dynamic" {
			add("score_type", "must be static or dynamic")
		}
		if evaluation == "graded" && scoreType == "dynamic" {
			add("score_type", "graded evaluation requires static scoring")
		}
		if _, err := importInt(row["score_minimum"], points, 0, points); err != nil {
			add("score_minimum", err.Error())
		}
		if _, err := importInt(row["score_decay"], 50, 1, 1000000); err != nil {
			add("score_decay", err.Error())
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

func (h *DataHandler) provisionImportedUsers(ctx context.Context, tx pgx.Tx, rows []map[string]string, plan importPlan, createdBy uuid.UUID) (int, error) {
	if h.mailSvc == nil || !h.mailSvc.Ready(ctx) {
		return 0, errors.New("mail delivery is not ready; test an active provider and preview the import again")
	}
	publicURL, eventName, err := importEventIdentity(ctx, tx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for index, rowPlan := range plan.Rows {
		if rowPlan.Action != "create" {
			continue
		}
		row := rows[index]
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE users SET email_verified = FALSE WHERE username = $1 RETURNING id`, row["username"]).Scan(&userID); err != nil {
			return 0, err
		}
		token, err := generateSecureToken(32)
		if err != nil {
			return 0, err
		}
		digest := sha256.Sum256([]byte(token))
		expiresAt := time.Now().UTC().Add(48 * time.Hour)
		if _, err := tx.Exec(ctx, `UPDATE account_activation_tokens SET used_at = NOW() WHERE user_id = $1 AND purpose = 'activation' AND used_at IS NULL`, userID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_activation_tokens (user_id, purpose, token_hash, expires_at, created_by) VALUES ($1, 'activation', $2, $3, $4)`, userID, digest[:], expiresAt, createdBy); err != nil {
			return 0, err
		}
		participantName := strings.TrimSpace(row["display_name"])
		if participantName == "" {
			participantName = row["username"]
		}
		activationURL := publicURL + "/activate#token=" + url.QueryEscape(token)
		_, err = h.mailSvc.EnqueueTx(ctx, tx, row["email"], "account_activation", map[string]string{
			"participant_name": participantName,
			"event_name":       eventName,
			"activation_url":   activationURL,
			"username":         row["username"],
			"expires_at":       expiresAt.Format(time.RFC1123Z),
		}, &createdBy)
		if err != nil {
			return 0, err
		}
		queued++
	}
	return queued, nil
}

func (h *DataHandler) provisionImportedCredentials(ctx context.Context, tx pgx.Tx, rows []map[string]string, plan importPlan, createdBy uuid.UUID) (int, error) {
	if h.mailSvc == nil || !h.mailSvc.Ready(ctx) {
		return 0, errors.New("mail delivery is not ready; test an active provider and preview the import again")
	}
	publicURL, eventName, err := importEventIdentity(ctx, tx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for index, rowPlan := range plan.Rows {
		if rowPlan.Action != "create" {
			continue
		}
		row := rows[index]
		temporaryPassword, err := generateSecureToken(12)
		if err != nil {
			return 0, err
		}
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(temporaryPassword), bcrypt.DefaultCost)
		if err != nil {
			return 0, err
		}
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE users SET password_hash = $2, email_verified = FALSE, must_change_password = TRUE, updated_at = NOW() WHERE username = $1 RETURNING id`, row["username"], string(hashedPassword)).Scan(&userID); err != nil {
			return 0, err
		}
		participantName := strings.TrimSpace(row["display_name"])
		if participantName == "" {
			participantName = row["username"]
		}
		_, err = h.mailSvc.EnqueueTx(ctx, tx, row["email"], "account_credentials", map[string]string{
			"participant_name":   participantName,
			"event_name":         eventName,
			"login_url":          publicURL + "/login",
			"username":           row["username"],
			"temporary_password": temporaryPassword,
		}, &createdBy)
		if err != nil {
			return 0, err
		}
		queued++
	}
	return queued, nil
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
	minimum, _ := importInt(row["score_minimum"], points, 0, points)
	decay, _ := importInt(row["score_decay"], 50, 1, 1000000)
	scoreType := importDefault(row["score_type"], "static")
	if scoreType == "static" {
		minimum = points
	}
	privesc, _ := importBool(row["privesc"], false)
	release, err := importTime(row["release_date"])
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO challenges
		(slug, name, description, sub_description, difficulty, category_id, status, author_name, resource_type, delivery_type,
		 container_image, container_tag, cpu_limit, memory_limit, exposed_ports, base_points, score_type, score_minimum, score_decay, release_date, privesc, scoring_mode)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5::challenge_difficulty,
		 (SELECT id FROM categories WHERE slug = NULLIF($6, '')), $7::challenge_status, NULLIF($8, ''), $9::resource_type,
		 $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18, $19, $20, $21, $22)
	`, row["slug"], row["name"], row["description"], row["sub_description"], row["difficulty"], row["category_slug"], importDefault(row["status"], "draft"), row["author_name"], importDefault(row["resource_type"], "docker"), challengeImportDelivery(row), row["container_image"], challengeImportTag(row), importDefault(row["cpu_limit"], "1"), importDefault(row["memory_limit"], "512m"), importDefault(row["exposed_ports"], "[]"), points, scoreType, minimum, decay, release, privesc, importDefault(row["scoring_mode"], "flag"))
	return err
}

func updateChallengeImport(ctx context.Context, tx pgx.Tx, row map[string]string) error {
	points, _ := importInt(row["base_points"], 100, 0, 1000000)
	minimum, _ := importInt(row["score_minimum"], points, 0, points)
	decay, _ := importInt(row["score_decay"], 50, 1, 1000000)
	scoreType := importDefault(row["score_type"], "static")
	if scoreType == "static" {
		minimum = points
	}
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
		 score_type = $17, score_minimum = $18, score_decay = $19,
		 release_date = $20, privesc = $21, scoring_mode = $22, updated_at = NOW()
		WHERE slug = $1
	`, row["slug"], row["name"], row["description"], row["sub_description"], row["difficulty"], row["category_slug"], importDefault(row["status"], "draft"), row["author_name"], importDefault(row["resource_type"], "docker"), challengeImportDelivery(row), row["container_image"], challengeImportTag(row), importDefault(row["cpu_limit"], "1"), importDefault(row["memory_limit"], "512m"), importDefault(row["exposed_ports"], "[]"), points, scoreType, minimum, decay, release, privesc, importDefault(row["scoring_mode"], "flag"))
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

func challengeImportTag(row map[string]string) string {
	if value := strings.TrimSpace(row["container_tag"]); value != "" {
		return value
	}
	if strings.Contains(strings.TrimSpace(row["container_image"]), "@sha256:") {
		return ""
	}
	return "latest"
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
