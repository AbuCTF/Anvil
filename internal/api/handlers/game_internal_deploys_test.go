package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDeploymentEventRejectsOversizedBodyBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/arena/deployment-events",
		strings.NewReader(`{"team_id":"00000000-0000-0000-0000-000000000001","service":"`+strings.Repeat("x", 5000)+`","source_sequence":1,"state":"queued"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	(&GameInternalHandler{}).DeploymentEvent(ctx)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized deployment event = %d %s, want 400", response.Code, response.Body.String())
	}
}
