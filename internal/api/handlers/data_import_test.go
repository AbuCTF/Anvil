package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
)

func TestGeneratedUsernameBase(t *testing.T) {
	tests := map[string]string{
		"Avery.Rao+event@example.test": "avery_rao_event",
		"x@example.test":               "participant_x",
		"@example.test":                "ignored_name",
		"":                             "team_alpha",
	}
	for email, expected := range tests {
		displayName := "Team Alpha"
		if email != "" {
			displayName = "Ignored Name"
		}
		if actual := generatedUsernameBase(email, displayName); actual != expected {
			t.Fatalf("generatedUsernameBase(%q) = %q, want %q", email, actual, expected)
		}
	}
}

func TestParseImportWorkbook(t *testing.T) {
	workbook := excelize.NewFile()
	if err := workbook.SetSheetName("Sheet1", "Participants"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]any{
		{"username", "email", "display_name", "role", "status", "email_verified"},
		{"player_one", "one@example.test", "Player One", "user", "active", false},
		{"player_two", "two@example.test"},
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := workbook.SetCellValue("Participants", cell, value); err != nil {
				t.Fatalf("set %s: %v", cell, err)
			}
		}
	}
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	_ = workbook.Close()
	parsed, issues := parseImportWorkbook(buffer.Bytes(), "Participants")
	if len(issues) != 0 || len(parsed) != 2 {
		t.Fatalf("rows=%v issues=%v", parsed, issues)
	}
	if parsed[1]["username"] != "player_two" || parsed[1]["display_name"] != "" || parsed[1]["email_verified"] != "" {
		t.Fatalf("short row was not padded correctly: %v", parsed[1])
	}
	if _, issues := parseImportWorkbook(buffer.Bytes(), "Missing"); len(issues) != 1 || issues[0].Field != "sheet" {
		t.Fatalf("missing sheet issues=%v", issues)
	}
}

func TestParseImportWorkbookColumnMapping(t *testing.T) {
	workbook := excelize.NewFile()
	rows := [][]any{
		{"Employee ID", "Full Name", "Email Address", "Department"},
		{"ORG-1042", "Avery Rao", "avery@example.test", "Risk"},
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := workbook.SetCellValue("Sheet1", cell, value); err != nil {
				t.Fatalf("set %s: %v", cell, err)
			}
		}
	}
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	_ = workbook.Close()
	parsed, issues := parseImportWorkbookForEntity("users", buffer.Bytes(), "Sheet1", map[string]string{
		"employee_id": "username", "full_name": "display_name", "email_address": "email", "department": "",
	})
	if len(issues) != 0 || len(parsed) != 1 {
		t.Fatalf("rows=%v issues=%v", parsed, issues)
	}
	if parsed[0]["username"] != "ORG-1042" || parsed[0]["display_name"] != "Avery Rao" || parsed[0]["email"] != "avery@example.test" {
		t.Fatalf("column mapping failed: %v", parsed[0])
	}
	if _, exists := parsed[0]["department"]; exists {
		t.Fatalf("ignored column was imported: %v", parsed[0])
	}
}

func TestInspectWorkbookRecommendsMatchingSheet(t *testing.T) {
	workbook := excelize.NewFile()
	if err := workbook.SetSheetName("Sheet1", "Notes"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	if _, err := workbook.NewSheet("Participants"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	for column, value := range []string{"username", "email", "display_name", "role", "status", "email_verified"} {
		cell, _ := excelize.CoordinatesToCellName(column+1, 1)
		_ = workbook.SetCellValue("Participants", cell, value)
	}
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	_ = workbook.Close()
	payload, _ := json.Marshal(map[string]string{"entity": "users", "content": base64.StdEncoding.EncodeToString(buffer.Bytes())})
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPost, "/imports/workbook/inspect", bytes.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	NewDataHandler(nil, zap.NewNop()).InspectWorkbook(context)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"recommended_sheet":"Participants"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInspectAndMapCSVAndJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewDataHandler(nil, zap.NewNop())
	for _, test := range []struct {
		format  string
		content string
	}{
		{format: "csv", content: "Employee Number,Work Email,Full Name,Department\nEMP-17,avery@example.test,Avery Rao,Risk\n"},
		{format: "json", content: `[{"Employee Number":"EMP-17","Work Email":"avery@example.test","Full Name":"Avery Rao","Department":"Risk"}]`},
	} {
		payload, _ := json.Marshal(map[string]string{"entity": "users", "format": test.format, "content": test.content})
		response := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(response)
		context.Request = httptest.NewRequest(http.MethodPost, "/imports/inspect", bytes.NewReader(payload))
		context.Request.Header.Set("Content-Type", "application/json")
		handler.InspectImport(context)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"Employee Number"`) {
			t.Fatalf("%s inspection status=%d body=%s", test.format, response.Code, response.Body.String())
		}
		mapping := map[string]string{"employee_number": "username", "work_email": "email", "full_name": "display_name", "department": ""}
		rows, issues := parseImportContent("users", test.format, []byte(test.content), "", mapping)
		if len(issues) != 0 || len(rows) != 1 {
			t.Fatalf("%s rows=%v issues=%v", test.format, rows, issues)
		}
		if rows[0]["username"] != "EMP-17" || rows[0]["email"] != "avery@example.test" || rows[0]["display_name"] != "Avery Rao" {
			t.Fatalf("%s mapping failed: %v", test.format, rows[0])
		}
		if _, exists := rows[0]["department"]; exists {
			t.Fatalf("%s ignored column was imported: %v", test.format, rows[0])
		}
	}
}

func TestImportTemplatesSupportCSVJSONAndExcel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewDataHandler(nil, zap.NewNop())
	for _, format := range []string{"csv", "json", "xlsx"} {
		response := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(response)
		context.Params = gin.Params{{Key: "entity", Value: "users"}}
		context.Request = httptest.NewRequest(http.MethodGet, "/templates/users?format="+format, nil)
		handler.Template(context)
		if response.Code != http.StatusOK || len(response.Body.Bytes()) == 0 {
			t.Fatalf("%s template status=%d size=%d body=%s", format, response.Code, response.Body.Len(), response.Body.String())
		}
		if !strings.Contains(response.Header().Get("Content-Disposition"), "."+format) {
			t.Fatalf("%s template disposition=%q", format, response.Header().Get("Content-Disposition"))
		}
	}
}

func TestChallengeImportTagPreservesDigestPins(t *testing.T) {
	tests := []struct {
		row  map[string]string
		want string
	}{
		{row: map[string]string{"container_image": "ghcr.io/acme/challenge@sha256:abc"}, want: ""},
		{row: map[string]string{"container_image": "acme/challenge"}, want: "latest"},
		{row: map[string]string{"container_image": "acme/challenge", "container_tag": "stable"}, want: "stable"},
	}
	for _, test := range tests {
		if got := challengeImportTag(test.row); got != test.want {
			t.Fatalf("challengeImportTag(%v) = %q, want %q", test.row, got, test.want)
		}
	}
}
