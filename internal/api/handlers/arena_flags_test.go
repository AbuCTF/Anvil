package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestBulkSubmitErrorLogHashesFlag(t *testing.T) {
	const flag = "H7CTF{0123456789abcdef0123456789abcdef}"
	core, observed := observer.New(zap.ErrorLevel)
	logBulkSubmitError(zap.New(core), errors.New("database unavailable"), flag)

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if strings.Contains(entries[0].Message, flag) {
		t.Fatal("raw submitted flag appeared in log message")
	}
	fields := entries[0].ContextMap()
	for _, value := range fields {
		if strings.Contains(fmt.Sprint(value), flag) {
			t.Fatal("raw submitted flag appeared in structured log field")
		}
	}
	digest := sha256.Sum256([]byte(flag))
	if got := fields["flag_sha256"]; got != hex.EncodeToString(digest[:]) {
		t.Fatalf("flag_sha256 = %v, want full SHA-256", got)
	}
}

func TestArenaFlagHandlersRejectOversizedInputBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &GameHandler{config: &config.Config{Game: config.GameConfig{Enabled: true}}, logger: zap.NewNop()}
	oversized := strings.Repeat("x", maximumArenaFlagLength+1)

	t.Run("single", func(t *testing.T) {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Set("user_id", uuid.New())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/arena/submit",
			strings.NewReader(`{"flag":"`+oversized+`"}`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler.SubmitFlag(ctx)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid flag") {
			t.Fatalf("single oversized flag = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("bulk", func(t *testing.T) {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		teamID := uuid.New()
		ctx.Set("game_team_id", teamID)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/arena/flags",
			bytes.NewBufferString(`["`+oversized+`"]`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler.BulkSubmitFlags(ctx)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "between 1 and 128") {
			t.Fatalf("bulk oversized flag = %d %s", response.Code, response.Body.String())
		}
	})
}
