//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Destructive: truncates the configured *_test database.
func TestNotificationAudienceReceiptsAndAdminLifecycle(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 8, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}

	alphaTeam, bravoTeam := uuid.New(), uuid.New()
	alpha, bravo, admin, author := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`TRUNCATE users, teams CASCADE`)
	exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, 'Alpha', 'a'), ($2, 'Bravo', 'b')`, alphaTeam, bravoTeam)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'alpha', 'alpha@x', 'user', 'active', $5),
		($2, 'bravo', 'bravo@x', 'user', 'active', $6),
		($3, 'admin', 'admin@x', 'admin', 'active', NULL),
		($4, 'author', 'author@x', 'author', 'active', NULL)`,
		alpha, bravo, admin, author, alphaTeam, bravoTeam)
	exec(`INSERT INTO platform_settings (key, value) VALUES ('economy_mode', 'true')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO economy_team_score (team_id, points, credits, grant_issued, bailout_used, p2c_blocks)
		VALUES ($1, 0, 0, true, false, 0)`, alphaTeam)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "notification-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	token := func(uid uuid.UUID) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	request := func(method, path string, uid *uuid.UUID, body any) (int, map[string]any, http.Header) {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if uid != nil {
			req.Header.Set("Authorization", "Bearer "+token(*uid))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Header()
	}

	if code, _, _ := request(http.MethodGet, "/api/v1/notifications", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d, want 401", code)
	}
	if code, _, _ := request(http.MethodPost, "/api/v1/admin/announcements", &alpha, map[string]any{"title": "x", "body": "y"}); code != http.StatusForbidden {
		t.Fatalf("participant admin create = %d, want 403", code)
	}

	create := func(title, audience string) uuid.UUID {
		t.Helper()
		code, body, _ := request(http.MethodPost, "/api/v1/admin/announcements", &admin, map[string]any{
			"title": title, "body": title + " body", "severity": "warning", "audience": audience, "pinned": true,
		})
		if code != http.StatusCreated {
			t.Fatalf("create %s = %d: %v", title, code, body)
		}
		return uuid.MustParse(body["id"].(string))
	}
	allID := create("Everyone", "all")
	create("Players", "participants")
	create("Staff", "staff")
	if code, body, _ := request(http.MethodPost, "/api/v1/economy/bailout", &alpha, nil); code != http.StatusOK {
		t.Fatalf("bailout notification producer = %d: %v", code, body)
	}
	if code, _, _ := request(http.MethodPost, "/api/v1/economy/bailout", &alpha, nil); code != http.StatusConflict {
		t.Fatalf("duplicate bailout = %d, want 409", code)
	}

	alphaEvent, bravoEvent, alphaDirect := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO notification_items
		(id, kind, event_type, title, body, severity, audience, team_id, user_id, dedup_key) VALUES
		($1, 'event', 'economy.opened', 'Alpha team event', 'private', 'info', 'team', $4, NULL, 'alpha-team-event'),
		($2, 'event', 'economy.opened', 'Bravo team event', 'private', 'info', 'team', $5, NULL, 'bravo-team-event'),
		($3, 'event', 'grader.result', 'Alpha direct event', 'private', 'success', 'user', NULL, $6, 'alpha-direct-event')`,
		alphaEvent, bravoEvent, alphaDirect, alphaTeam, bravoTeam, alpha)
	// Hidden lifecycle states must never reach the participant stream.
	exec(`INSERT INTO notification_items
		(kind, event_type, title, body, severity, audience, publish_at, expires_at) VALUES
		('announcement', 'organizer.announcement', 'Scheduled', 'future', 'info', 'all', NOW() + INTERVAL '1 hour', NULL),
		('announcement', 'organizer.announcement', 'Expired', 'past', 'info', 'all', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour'),
		('announcement', 'organizer.announcement', 'Cancelled', 'gone', 'info', 'all', NOW() - INTERVAL '1 hour', NULL)`)
	exec(`UPDATE notification_items SET cancelled_at = NOW() WHERE title = 'Cancelled'`)

	code, alphaList, header := request(http.MethodGet, "/api/v1/notifications?limit=100", &alpha, nil)
	if code != http.StatusOK {
		t.Fatalf("alpha list = %d: %v", code, alphaList)
	}
	if header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("cache control = %q", header.Get("Cache-Control"))
	}
	assertTitles := func(body map[string]any, want []string) {
		t.Helper()
		got := map[string]bool{}
		rawItems := body["items"].([]any)
		for _, raw := range rawItems {
			got[raw.(map[string]any)["title"].(string)] = true
		}
		if len(rawItems) != len(want) || len(got) != len(want) {
			t.Fatalf("titles = %v, want %v", got, want)
		}
		for _, title := range want {
			if !got[title] {
				t.Fatalf("titles = %v, missing %q", got, title)
			}
		}
	}
	assertTitles(alphaList, []string{"Everyone", "Players", "Bailout claimed", "Alpha team event", "Alpha direct event"})
	if alphaList["unread_count"] != 5.0 {
		t.Fatalf("alpha unread = %v, want 5", alphaList["unread_count"])
	}

	_, bravoList, _ := request(http.MethodGet, "/api/v1/notifications?limit=100", &bravo, nil)
	assertTitles(bravoList, []string{"Everyone", "Players", "Bravo team event"})
	_, staffList, _ := request(http.MethodGet, "/api/v1/notifications?limit=100", &author, nil)
	assertTitles(staffList, []string{"Everyone", "Staff"})

	if code, _, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/notifications/%s/read", bravoEvent), &alpha, nil); code != http.StatusNotFound {
		t.Fatalf("cross-team mark read = %d, want 404", code)
	}
	if code, _, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/notifications/%s/read", alphaEvent), &alpha, nil); code != http.StatusOK {
		t.Fatalf("mark read = %d, want 200", code)
	}
	_, countBody, _ := request(http.MethodGet, "/api/v1/notifications/unread-count", &alpha, nil)
	if countBody["unread_count"] != 4.0 {
		t.Fatalf("alpha unread after one = %v, want 4", countBody)
	}
	if code, _, _ := request(http.MethodPost, "/api/v1/notifications/read-all", &alpha, nil); code != http.StatusOK {
		t.Fatalf("mark all = %d, want 200", code)
	}
	_, countBody, _ = request(http.MethodGet, "/api/v1/notifications/unread-count", &alpha, nil)
	if countBody["unread_count"] != 0.0 {
		t.Fatalf("alpha unread after all = %v, want 0", countBody)
	}

	if code, _, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/admin/announcements/%s/cancel", allID), &admin, nil); code != http.StatusOK {
		t.Fatalf("cancel = %d, want 200", code)
	}
	_, alphaList, _ = request(http.MethodGet, "/api/v1/notifications?limit=100", &alpha, nil)
	assertTitles(alphaList, []string{"Players", "Bailout claimed", "Alpha team event", "Alpha direct event"})
	code, adminHistory, _ := request(http.MethodGet, "/api/v1/admin/announcements", &admin, nil)
	if code != http.StatusOK || len(adminHistory["announcements"].([]any)) != 6 {
		t.Fatalf("admin history = %d: %v", code, adminHistory)
	}
}
