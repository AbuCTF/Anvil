package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func requestGameState(handler *GameHandler, etag string) *httptest.ResponseRecorder {
	return requestGameStateAs(handler, etag, "")
}

func requestGameStateAs(handler *GameHandler, etag, role string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/arena/state", nil)
	if role != "" {
		ctx.Set("role", role)
	}
	if etag != "" {
		ctx.Request.Header.Set("If-None-Match", etag)
	}
	handler.State(ctx)
	ctx.Writer.WriteHeaderNow()
	return recorder
}

func TestStaffPreviewIncludesHiddenTeamsWithoutPopulatingPublicCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var includeHidden bool
	var loads atomic.Int32
	handler := NewGameHandler(&config.Config{Game: config.GameConfig{Enabled: true}}, nil, zap.NewNop())
	handler.arenaOpen = func(context.Context) (bool, error) { return false, nil }
	handler.stateLoad = func(_ context.Context, include bool) ([]byte, string, error) {
		loads.Add(1)
		includeHidden = include
		return encodeGameState(gin.H{
			"active":    true,
			"standings": []gameStanding{{TeamID: "qa", Team: "zz-qa-codex", Koth: 12, Total: 12}},
		})
	}

	preview := requestGameStateAs(handler, "", "admin")
	if preview.Code != http.StatusOK || preview.Header().Get("X-Anvil-Cache") != "PREVIEW" {
		t.Fatalf("preview response = %d %q, want 200 PREVIEW", preview.Code, preview.Header().Get("X-Anvil-Cache"))
	}
	if !includeHidden {
		t.Fatal("staff preview did not request hidden QA teams")
	}
	if got := preview.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("preview Cache-Control = %q, want no-store", got)
	}
	if !json.Valid(preview.Body.Bytes()) || !bytes.Contains(preview.Body.Bytes(), []byte("zz-qa-codex")) {
		t.Fatalf("preview body does not contain QA standing: %s", preview.Body.String())
	}
	handler.stateMu.Lock()
	cached := len(handler.stateCache.body)
	handler.stateMu.Unlock()
	if cached != 0 {
		t.Fatal("staff-only preview entered the shared public cache")
	}

	public := requestGameState(handler, "")
	if public.Code != http.StatusOK || public.Header().Get("X-Anvil-Cache") != "IDLE" {
		t.Fatalf("public response = %d %q, want 200 IDLE", public.Code, public.Header().Get("X-Anvil-Cache"))
	}
	if bytes.Contains(public.Body.Bytes(), []byte("zz-qa-codex")) {
		t.Fatal("public inactive response leaked the QA team")
	}
	if loads.Load() != 1 {
		t.Fatalf("live loads = %d, want only the staff preview load", loads.Load())
	}
}

func TestOpenArenaAdminUsesFilteredPublicCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var includeHidden bool
	handler := NewGameHandler(&config.Config{Game: config.GameConfig{Enabled: true}}, nil, zap.NewNop())
	handler.arenaOpen = func(context.Context) (bool, error) { return true, nil }
	handler.stateLoad = func(_ context.Context, include bool) ([]byte, string, error) {
		includeHidden = include
		return encodeGameState(gin.H{"active": true, "standings": []gameStanding{}})
	}

	response := requestGameStateAs(handler, "", "admin")
	if response.Code != http.StatusOK || response.Header().Get("X-Anvil-Cache") != "MISS" {
		t.Fatalf("response = %d %q, want 200 MISS", response.Code, response.Header().Get("X-Anvil-Cache"))
	}
	if includeHidden {
		t.Fatal("open arena admin request included hidden QA teams")
	}
}

func liveStateBody(t *testing.T, tick int) ([]byte, string) {
	t.Helper()
	body, etag, err := encodeGameState(gin.H{
		"active":    true,
		"status":    gin.H{"tick": tick, "round": 0, "tick_interval_seconds": 120},
		"hills":     []gameHill{},
		"standings": []gameStanding{},
		"services":  []matrixService{},
		"rows":      []matrixRow{},
		"events":    []gameEvent{},
		"history":   []*historySeries{},
	})
	if err != nil {
		t.Fatalf("encode live state: %v", err)
	}
	return body, etag
}

func TestGameStateWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/arena/state", nil)

	handler := NewGameHandler(&config.Config{
		Game: config.GameConfig{TickInterval: 2 * time.Minute},
	}, nil, zap.NewNop())
	handler.State(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=15, stale-if-error=10" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Anvil-Cache"); got != "IDLE" {
		t.Fatalf("X-Anvil-Cache = %q, want IDLE", got)
	}
	var payload struct {
		Active    bool           `json:"active"`
		Standings []gameStanding `json:"standings"`
		Status    struct {
			TickIntervalSeconds int `json:"tick_interval_seconds"`
		} `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if payload.Active {
		t.Fatal("disabled arena reported active")
	}
	if payload.Standings == nil || len(payload.Standings) != 0 {
		t.Fatalf("standings = %#v, want an empty array", payload.Standings)
	}
	if payload.Status.TickIntervalSeconds != 120 {
		t.Fatalf("tick interval = %d, want 120", payload.Status.TickIntervalSeconds)
	}

	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("disabled arena response is missing an ETag")
	}
	revalidated := httptest.NewRecorder()
	revalidatedCtx, _ := gin.CreateTestContext(revalidated)
	revalidatedCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/arena/state", nil)
	revalidatedCtx.Request.Header.Set("If-None-Match", etag)
	handler.State(revalidatedCtx)
	revalidatedCtx.Writer.WriteHeaderNow()
	if revalidated.Code != http.StatusNotModified {
		t.Fatalf("revalidated status = %d, want %d", revalidated.Code, http.StatusNotModified)
	}
	if revalidated.Body.Len() != 0 {
		t.Fatalf("revalidated body = %q, want empty", revalidated.Body.String())
	}
}

func TestLiveGameStateMissHitRevalidateAndRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var loads atomic.Int32
	handler := NewGameHandler(&config.Config{Game: config.GameConfig{Enabled: true}}, nil, zap.NewNop())
	handler.stateLoad = func(context.Context, bool) ([]byte, string, error) {
		body, etag := liveStateBody(t, int(loads.Add(1)))
		return body, etag, nil
	}

	first := requestGameState(handler, "")
	if first.Code != http.StatusOK || first.Header().Get("X-Anvil-Cache") != "MISS" {
		t.Fatalf("first response = %d %q, want 200 MISS", first.Code, first.Header().Get("X-Anvil-Cache"))
	}
	if got := first.Header().Get("Cache-Control"); got != "public, max-age=5, stale-if-error=10" {
		t.Fatalf("Cache-Control = %q", got)
	}
	etag := first.Header().Get("ETag")
	second := requestGameState(handler, "")
	if second.Code != http.StatusOK || second.Header().Get("X-Anvil-Cache") != "HIT" {
		t.Fatalf("second response = %d %q, want 200 HIT", second.Code, second.Header().Get("X-Anvil-Cache"))
	}
	revalidated := requestGameState(handler, etag)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Fatalf("revalidated response = %d %q, want empty 304", revalidated.Code, revalidated.Body.String())
	}

	handler.stateMu.Lock()
	handler.stateCache.expiresAt = time.Now().Add(-time.Second)
	handler.stateMu.Unlock()
	refreshed := requestGameState(handler, "")
	if refreshed.Code != http.StatusOK || refreshed.Header().Get("X-Anvil-Cache") != "MISS" {
		t.Fatalf("refreshed response = %d %q, want 200 MISS", refreshed.Code, refreshed.Header().Get("X-Anvil-Cache"))
	}
	if refreshed.Header().Get("ETag") == etag {
		t.Fatal("expired state retained its old ETag after refresh")
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("loads = %d, want 2", got)
	}
}

func TestLiveGameStateCoalescesConcurrentMisses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var loads atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := NewGameHandler(&config.Config{Game: config.GameConfig{Enabled: true}}, nil, zap.NewNop())
	handler.stateLoad = func(context.Context, bool) ([]byte, string, error) {
		loads.Add(1)
		once.Do(func() { close(entered) })
		<-release
		body, etag := liveStateBody(t, 1)
		return body, etag, nil
	}

	const requests = 16
	results := make(chan *httptest.ResponseRecorder, requests)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- requestGameState(handler, "")
		}()
	}
	close(start)
	<-entered
	close(release)
	wg.Wait()
	close(results)

	if got := loads.Load(); got != 1 {
		t.Fatalf("loads = %d, want one coalesced load", got)
	}
	for recorder := range results {
		if recorder.Code != http.StatusOK {
			t.Fatalf("concurrent response = %d, want 200", recorder.Code)
		}
	}
}

func TestLiveGameStateBoundsAndSurfacesStaleFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, etag := liveStateBody(t, 7)
	loadErr := errors.New("database unavailable")
	handler := NewGameHandler(&config.Config{Game: config.GameConfig{Enabled: true}}, nil, zap.NewNop())
	handler.stateLoad = func(context.Context, bool) ([]byte, string, error) { return nil, "", loadErr }
	handler.stateCache = gameStateCacheEntry{
		body:      body,
		etag:      etag,
		expiresAt: time.Now().Add(-time.Second),
	}

	stale := requestGameState(handler, "")
	if stale.Code != http.StatusOK || stale.Header().Get("X-Anvil-Cache") != "STALE" {
		t.Fatalf("stale response = %d %q, want 200 STALE", stale.Code, stale.Header().Get("X-Anvil-Cache"))
	}
	if got := stale.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("stale Cache-Control = %q, want no-store", got)
	}

	handler.stateMu.Lock()
	handler.stateCache.expiresAt = time.Now().Add(-arenaStaleTTL - time.Second)
	handler.stateMu.Unlock()
	unavailable := requestGameState(handler, "")
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("over-age stale response = %d, want 503", unavailable.Code)
	}
}
