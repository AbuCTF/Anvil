package handlers

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestScoreboardResponseCacheHitAndExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ScoreboardHandler{logger: zap.NewNop()}
	payload := gin.H{"leaderboard": []string{"first"}}

	ctx, response := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	handler.respondCacheableJSON(ctx, "scoreboard:1:100", 2*time.Second, payload)
	if response.Code != http.StatusOK {
		t.Fatalf("cache miss status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("X-Anvil-Cache"); got != "MISS" {
		t.Fatalf("cache miss header = %q, want MISS", got)
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("cache miss omitted ETag")
	}
	missBody := response.Body.String()

	ctx, response = testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	ctx.Set(scoreboardPublicContextKey, true)
	if !handler.serveCachedJSON(ctx, "scoreboard:1:100") {
		t.Fatal("expected cached response")
	}
	if got := response.Header().Get("X-Anvil-Cache"); got != "HIT" {
		t.Fatalf("cache hit header = %q, want HIT", got)
	}
	if got := response.Body.String(); got != missBody {
		t.Fatalf("cache hit body = %q, want %q", got, missBody)
	}
	if got := response.Header().Get("Cache-Control"); got == "public, max-age=2" || !strings.HasPrefix(got, "public, max-age=") {
		t.Fatalf("cache hit control = %q, want public remaining TTL", got)
	}

	ctx, response = testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	ctx.Set(scoreboardPublicContextKey, true)
	ctx.Request.Header.Set("If-None-Match", etag)
	if !handler.serveCachedJSON(ctx, "scoreboard:1:100") {
		t.Fatal("expected conditional cached response")
	}
	if ctx.Writer.Status() != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want %d", ctx.Writer.Status(), http.StatusNotModified)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("conditional response included %d body bytes", response.Body.Len())
	}

	handler.cacheMu.Lock()
	entry := handler.cache["scoreboard:1:100"]
	entry.expiresAt = time.Now().Add(-time.Second)
	handler.cache["scoreboard:1:100"] = entry
	handler.cacheMu.Unlock()

	ctx, _ = testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	if handler.serveCachedJSON(ctx, "scoreboard:1:100") {
		t.Fatal("expired response was served")
	}
	if _, ok := handler.cache["scoreboard:1:100"]; ok {
		t.Fatal("expired response was not removed")
	}
}

func TestScoreboardCacheFillCoalescesWaiters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ScoreboardHandler{logger: zap.NewNop()}
	leaderCtx, leaderResponse := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	if !handler.beginCacheFill(leaderCtx, "matrix") {
		t.Fatal("first cache miss was not elected leader")
	}

	waiterCtx, waiterResponse := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	waiterDone := make(chan bool, 1)
	go func() { waiterDone <- handler.beginCacheFill(waiterCtx, "matrix") }()
	deadline := time.Now().Add(time.Second)
	for {
		handler.flightMu.Lock()
		waiters := handler.flights["matrix"].waiters
		handler.flightMu.Unlock()
		if waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache waiter did not join the in-flight request")
		}
		runtime.Gosched()
	}

	handler.respondCacheableJSON(leaderCtx, "matrix", time.Second, gin.H{"rows": []int{1}})
	if leaderResponse.Code != http.StatusOK {
		t.Fatalf("leader status = %d, want %d", leaderResponse.Code, http.StatusOK)
	}
	handler.finishCacheFill("matrix")

	select {
	case becameLeader := <-waiterDone:
		if becameLeader {
			t.Fatal("waiter ran a duplicate cache fill")
		}
	case <-time.After(time.Second):
		t.Fatal("cache waiter did not resume")
	}
	if waiterResponse.Header().Get("X-Anvil-Cache") != "HIT" {
		t.Fatalf("waiter cache header = %q, want HIT", waiterResponse.Header().Get("X-Anvil-Cache"))
	}
}

func TestScoreboardAccessHonorsEnabledAndPublicSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ScoreboardHandler{}

	ctx, response := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	if handler.allowScoreboardRequest(ctx, false, true) || response.Code != http.StatusNotFound {
		t.Fatalf("disabled scoreboard status = %d, want %d", response.Code, http.StatusNotFound)
	}

	ctx, response = testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	if handler.allowScoreboardRequest(ctx, true, false) || response.Code != http.StatusUnauthorized {
		t.Fatalf("private anonymous scoreboard status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	ctx, response = testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
	ctx.Set("user_id", uuid.New())
	if !handler.allowScoreboardRequest(ctx, true, false) {
		t.Fatalf("authenticated private scoreboard status = %d, want allowed", response.Code)
	}
}

func TestScoreboardResponseCacheStaysBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ScoreboardHandler{logger: zap.NewNop()}

	for i := 0; i < maxScoreboardCacheEntries+20; i++ {
		ctx, _ := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")
		handler.respondCacheableJSON(ctx, string(rune(i)), time.Minute, gin.H{"rank": i})
	}
	if got := len(handler.cache); got != maxScoreboardCacheEntries {
		t.Fatalf("cache entries = %d, want %d", got, maxScoreboardCacheEntries)
	}
}

func TestScoreboardResponseCacheMarshalFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ScoreboardHandler{logger: zap.NewNop()}
	ctx, response := testHandlerContext(http.MethodGet, "/api/v1/scoreboard")

	handler.respondCacheableJSON(ctx, "invalid", time.Second, gin.H{"unsupported": make(chan int)})

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if _, ok := handler.cache["invalid"]; ok {
		t.Fatal("unencodable payload was cached")
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != "failed to encode response" {
		t.Fatalf("error = %q, want safe encoding error", body["error"])
	}
}

func TestEscapeScoreboardSearchTreatsWildcardsLiterally(t *testing.T) {
	tests := map[string]string{
		"plain":       "plain",
		"percent%":    `percent\%`,
		"under_score": `under\_score`,
		`slash\value`: `slash\\value`,
	}
	for input, want := range tests {
		if got := escapeScoreboardSearch(input); got != want {
			t.Errorf("escapeScoreboardSearch(%q) = %q, want %q", input, got, want)
		}
	}
}
