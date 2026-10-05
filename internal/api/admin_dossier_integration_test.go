//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestAdminDossiersUseAuthoritativeCompetitionData(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	adminID, userID, teamID, challengeID, flagID, categoryID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	instanceID, submissionID := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'admin', 'active')`, adminID, "admin-"+adminID.String(), adminID.String()+"@example.test")
	exec(`INSERT INTO categories (id, name, slug) VALUES ($1, 'Integration category', $2)`, categoryID, "category-"+categoryID.String())
	exec(`INSERT INTO teams (id, name, join_code, total_score, created_by) VALUES ($1, $2, $3, 0, $4)`, teamID, "team-"+teamID.String(), "join-"+teamID.String(), adminID)
	exec(`INSERT INTO users (id, username, email, role, status, team_id, last_login_at, last_login_ip) VALUES ($1, $2, $3, 'user', 'active', $4, NOW(), '203.0.113.10')`, userID, "user-"+userID.String(), userID.String()+"@example.test", teamID)
	exec(`INSERT INTO challenges (id, name, slug, description, difficulty, status, container_image, base_points, total_flags) VALUES ($1, 'Dossier target', $2, 'test', 'medium', 'published', '', 100, 1)`, challengeID, "challenge-"+challengeID.String())
	exec(`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order) VALUES ($1, $2, 'Flag', 'digest', 100, 1)`, flagID, challengeID)
	exec(`INSERT INTO instances (id, challenge_id, user_id, team_id, status, ip_address, container_id, started_at, expires_at) VALUES ($1, $2, $3, $4, 'running', 'challenge.test', 'runtime', NOW(), NOW() + INTERVAL '1 hour')`, instanceID, challengeID, userID, teamID)
	exec(`INSERT INTO instance_flags (id, instance_id, user_id, challenge_id, flag_id, flag_value) VALUES ($1, $2, $3, $4, $5, 'H7CTF{runtime-secret}')`, uuid.New(), instanceID, userID, challengeID, flagID)
	exec(`INSERT INTO flag_share_events (id, challenge_id, flag_id, owner_user_id, owner_instance_id, submitter_user_id, flag_value, submitter_ip) VALUES ($1, $2, $3, $4, $5, $4, 'H7CTF{shared-secret}', '203.0.113.13')`, uuid.New(), challengeID, flagID, userID, instanceID)
	exec(`INSERT INTO submissions (id, user_id, challenge_id, flag_id, instance_id, submitted_flag, is_correct, points_awarded, ip_address, user_agent) VALUES ($1, $2, $3, $4, $5, 'H7CTF{correct}', true, 100, '203.0.113.10', 'qa-client'), ($6, $2, $3, $4, $5, 'H7CTF{wrong}', false, 0, '203.0.113.11', 'qa-client')`, submissionID, userID, challengeID, flagID, instanceID, uuid.New())
	exec(`INSERT INTO solves (id, user_id, challenge_id, flag_id, points_awarded) VALUES ($1, $2, $3, $4, 100)`, uuid.New(), userID, challengeID, flagID)
	exec(`INSERT INTO economy_team_score (team_id, points, credits, grant_issued) VALUES ($1, 321.500, 777.250, true)`, teamID)
	exec(`INSERT INTO economy_challenge_state (team_id, challenge_id, status, holds_solve, current_value, frac, opened_at) VALUES ($1, $2, 'solved', true, 321.500, 1, NOW())`, teamID, challengeID)
	exec(`INSERT INTO sessions (id, user_id, session_token, ip_address, user_agent, expires_at) VALUES ($1, $2, $3, '203.0.113.12', 'qa-browser', NOW() + INTERVAL '1 hour')`, uuid.New(), userID, uuid.NewString())
	authSessionID := uuid.New()
	exec(`INSERT INTO refresh_tokens (id, user_id, session_id, token_hash, expires_at, ip_address, user_agent, last_used_at) VALUES ($1, $2, $3, $4, NOW() + INTERVAL '1 hour', '203.0.113.15', 'qa-access', NOW())`, uuid.New(), userID, authSessionID, uuid.NewString())
	exec(`INSERT INTO notification_items (kind, event_type, title, body, severity, audience, user_id, pinned, created_by) VALUES ('announcement', 'organizer.warning', 'Organizer warning', 'integration warning', 'warning', 'user', $1, true, $2)`, userID, adminID)
	exec(`INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent) VALUES ($1, 'user_warned', 'user', $2, '{"reason":"integration"}', '203.0.113.1', 'qa-admin'), ($1, 'challenge_updated', 'challenge', $3, '{"status":"published"}', '203.0.113.1', 'qa-admin')`, adminID, userID, challengeID)
	exec(`INSERT INTO audit_log (user_id, action, entity_type, entity_id, ip_address, user_agent) VALUES ($1, 'user.login', 'user', $1, '203.0.113.14', 'qa-login')`, userID)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "admin-dossier-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	tokenFor := func(id uuid.UUID, username, role string) string {
		token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: id, Username: username, Role: role, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return token
	}
	tokenForSession := func(id, sessionID uuid.UUID, username, role string) string {
		token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: id, SessionID: sessionID, Username: username, Role: role, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return token
	}
	adminToken := tokenFor(adminID, "admin", "admin")
	userToken := tokenFor(userID, "user", "user")
	requestAs := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var payload *bytes.Reader
		if body == nil {
			payload = bytes.NewReader(nil)
		} else {
			encoded, _ := json.Marshal(body)
			payload = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, path, payload)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		return requestAs(method, path, adminToken, body)
	}
	decode := func(response *httptest.ResponseRecorder) map[string]any {
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return result
	}

	list := decode(request(http.MethodGet, "/api/v1/admin/teams", nil))
	teams := list["teams"].([]any)
	found := teams[0].(map[string]any)
	if found["ledger_points"].(float64) != 321.5 || found["challenge_solves"].(float64) != 1 || found["wrong_submissions"].(float64) != 1 {
		t.Fatalf("team list did not use authoritative data: %#v", found)
	}

	teamDetail := decode(request(http.MethodGet, "/api/v1/admin/teams/"+teamID.String()+"/detail", nil))
	if len(teamDetail["submissions"].([]any)) != 2 || len(teamDetail["access_ips"].([]any)) != 3 || len(teamDetail["solves"].([]any)) != 1 {
		t.Fatalf("incomplete team dossier: %#v", teamDetail)
	}
	support := teamDetail["support"].(map[string]any)
	if support["open_count"].(float64) != 0 || support["concurrency_cap"].(float64) < 1 {
		t.Fatalf("incomplete team support state: %#v", support)
	}
	for _, row := range teamDetail["submissions"].([]any) {
		if _, exposed := row.(map[string]any)["submitted_flag"]; exposed {
			t.Fatal("team dossier exposed raw flag material")
		}
	}

	userDetail := decode(request(http.MethodGet, "/api/v1/admin/users/"+userID.String()+"/detail", nil))
	accessSummary := userDetail["access_summary"].(map[string]any)
	if len(userDetail["sessions"].([]any)) != 1 || len(userDetail["auth_sessions"].([]any)) != 1 || len(userDetail["login_history"].([]any)) != 1 || len(userDetail["access_ips"].([]any)) != 5 || len(userDetail["submissions"].([]any)) != 2 || len(userDetail["warnings"].([]any)) != 1 || len(userDetail["audit"].([]any)) != 2 || accessSummary["login_count"].(float64) != 1 || accessSummary["active_sessions"].(float64) != 1 {
		t.Fatalf("incomplete user dossier: %#v", userDetail)
	}

	challengeDetail := decode(request(http.MethodGet, "/api/v1/admin/challenges/"+challengeID.String()+"/detail", nil))
	stats := challengeDetail["stats"].(map[string]any)
	if stats["submissions"].(float64) != 2 || stats["solved_teams"].(float64) != 1 || stats["running_instances"].(float64) != 1 || len(challengeDetail["audit"].([]any)) != 1 || len(challengeDetail["flags"].([]any)) != 1 {
		t.Fatalf("incomplete challenge dossier: %#v", challengeDetail)
	}
	for _, path := range []string{"/api/v1/admin/instance-flags", "/api/v1/admin/flag-shares"} {
		response := request(http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("audit evidence %s status=%d body=%s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		if strings.Contains(body, "runtime-secret") || strings.Contains(body, "shared-secret") || strings.Contains(body, "flag_value") || !strings.Contains(body, "flag_fingerprint") {
			t.Fatalf("audit evidence secret boundary failed for %s: %s", path, body)
		}
	}

	challengeName := "multi-" + uuid.NewString()
	createdResponse := request(http.MethodPost, "/api/v1/admin/challenges", map[string]any{
		"name": challengeName, "description": "integration", "difficulty": "hard", "base_points": 200,
		"category_id":    categoryID.String(),
		"challenge_type": "docker", "scoring_mode": "graded", "arena_mode": "per_team",
		"services": []any{
			map[string]any{"name": "app", "image": "ghcr.io/example/app", "public": true, "ports": []any{map[string]any{"port": 8080, "protocol": "tcp", "service": "http"}}},
			map[string]any{"name": "grader", "image": "ghcr.io/example/grader", "public": false, "egress": true},
		},
		"flags": []any{map[string]any{"name": "Depth", "flag": "H7CTF{depth}", "points": 200, "flag_type": "static"}},
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create multi-service status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(createdResponse.Body.Bytes(), &created)
	createdID := created["id"].(string)
	challengeList := decode(request(http.MethodGet, "/api/v1/admin/challenges", nil))
	var delivery string
	for _, item := range challengeList["challenges"].([]any) {
		challenge := item.(map[string]any)
		if challenge["id"] == createdID {
			delivery = challenge["delivery_type"].(string)
		}
	}
	if delivery != "multi" {
		t.Fatalf("delivery=%q, want multi", delivery)
	}
	metadata := request(http.MethodPatch, "/api/v1/admin/challenges/"+createdID+"/metadata", map[string]any{
		"description": "metadata-only edit", "difficulty": "medium", "base_points": 225,
	})
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata patch status=%d body=%s", metadata.Code, metadata.Body.String())
	}
	challengeList = decode(request(http.MethodGet, "/api/v1/admin/challenges", nil))
	for _, item := range challengeList["challenges"].([]any) {
		challenge := item.(map[string]any)
		if challenge["id"] == createdID {
			if challenge["category_id"] != categoryID.String() || challenge["delivery_type"] != "multi" || len(challenge["services"].([]any)) != 2 || challenge["base_points"].(float64) != 225 {
				t.Fatalf("metadata patch changed structural fields: %#v", challenge)
			}
		}
	}
	if response := requestAs(http.MethodGet, "/api/v1/admin/challenges", userToken, nil); response.Code != http.StatusForbidden {
		t.Fatalf("participant admin challenge list status=%d, want 403", response.Code)
	}
	if response := requestAs(http.MethodGet, "/api/v1/challenges/"+created["slug"].(string), userToken, nil); response.Code != http.StatusNotFound {
		t.Fatalf("participant draft preview status=%d, want 404", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/challenges/"+created["slug"].(string), nil); response.Code != http.StatusOK {
		t.Fatalf("admin draft preview status=%d body=%s", response.Code, response.Body.String())
	}
	publicDetail := requestAs(http.MethodGet, "/api/v1/challenges/challenge-"+challengeID.String(), userToken, nil)
	if publicDetail.Code != http.StatusOK {
		t.Fatalf("participant published detail status=%d body=%s", publicDetail.Code, publicDetail.Body.String())
	}
	for _, forbidden := range []string{"container_image", "flag_hash", "flag_value", "digest"} {
		if strings.Contains(publicDetail.Body.String(), forbidden) {
			t.Fatalf("participant detail exposed %q: %s", forbidden, publicDetail.Body.String())
		}
	}
	externalPayload := map[string]any{
		"name": challengeName, "description": "external integration", "difficulty": "hard", "base_points": 200,
		"resource_type": "docker", "delivery_type": "external", "scoring_mode": "flag", "arena_mode": "per_team",
		"container_image": "", "exposed_ports": []any{}, "services": []any{},
	}
	updated := request(http.MethodPut, "/api/v1/admin/challenges/"+createdID, externalPayload)
	if updated.Code != http.StatusOK {
		t.Fatalf("convert challenge to external status=%d body=%s", updated.Code, updated.Body.String())
	}
	challengeList = decode(request(http.MethodGet, "/api/v1/admin/challenges", nil))
	for _, item := range challengeList["challenges"].([]any) {
		challenge := item.(map[string]any)
		if challenge["id"] == createdID && challenge["delivery_type"] != "external" {
			t.Fatalf("delivery before handout=%q, want external", challenge["delivery_type"])
		}
	}
	link := request(http.MethodPost, "/api/v1/admin/challenges/"+createdID+"/attachments/link", map[string]any{
		"name": "artifact.zip", "url": "https://cdn.example.test/artifact.zip", "sha256": strings.Repeat("a", 64),
	})
	if link.Code != http.StatusCreated {
		t.Fatalf("external handout status=%d body=%s", link.Code, link.Body.String())
	}
	handouts := decode(request(http.MethodGet, "/api/v1/admin/challenges/"+createdID+"/attachments", nil))
	if len(handouts["attachments"].([]any)) != 1 {
		t.Fatalf("external handout was not listed: %#v", handouts)
	}
	challengeList = decode(request(http.MethodGet, "/api/v1/admin/challenges", nil))
	for _, item := range challengeList["challenges"].([]any) {
		challenge := item.(map[string]any)
		if challenge["id"] == createdID && challenge["delivery_type"] != "external" {
			t.Fatalf("external delivery with handout=%q, want external", challenge["delivery_type"])
		}
	}
	externalPayload["delivery_type"] = "static"
	updated = request(http.MethodPut, "/api/v1/admin/challenges/"+createdID, externalPayload)
	if updated.Code != http.StatusOK {
		t.Fatalf("convert challenge to static status=%d body=%s", updated.Code, updated.Body.String())
	}
	challengeList = decode(request(http.MethodGet, "/api/v1/admin/challenges", nil))
	for _, item := range challengeList["challenges"].([]any) {
		challenge := item.(map[string]any)
		if challenge["id"] == createdID && challenge["delivery_type"] != "static" {
			t.Fatalf("explicit static delivery=%q, want static", challenge["delivery_type"])
		}
	}

	credit := decode(request(http.MethodPost, "/api/v1/admin/teams/"+teamID.String()+"/credit", map[string]any{
		"kind": "admin_bonus", "amount": 2.75, "note": "integration verification",
	}))
	if credit["balance_after"].(float64) != 780 {
		t.Fatalf("credit balance=%v, want 780", credit["balance_after"])
	}
	var auditNote string
	if err := db.Pool.QueryRow(ctx, `SELECT new_values->>'note' FROM audit_log WHERE action = 'team_credit_adjusted' AND entity_id = $1`, teamID).Scan(&auditNote); err != nil || auditNote != "integration verification" {
		t.Fatalf("durable audit note=%q err=%v", auditNote, err)
	}
	teamDetail = decode(request(http.MethodGet, "/api/v1/admin/teams/"+teamID.String()+"/detail", nil))
	if len(teamDetail["credit_events"].([]any)) != 1 || len(teamDetail["audit"].([]any)) != 1 {
		t.Fatalf("team ledger or audit missing after adjustment: %#v", teamDetail)
	}

	sessionToken := tokenForSession(userID, authSessionID, "user", "user")
	if response := requestAs(http.MethodGet, "/api/v1/user/me", sessionToken, nil); response.Code != http.StatusOK {
		t.Fatalf("active account session status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v1/admin/users/"+userID.String()+"/sessions/"+authSessionID.String(), nil); response.Code != http.StatusOK {
		t.Fatalf("revoke one session status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(http.MethodGet, "/api/v1/user/me", sessionToken, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked account session status=%d, want 401 body=%s", response.Code, response.Body.String())
	}
	secondSessionID := uuid.New()
	exec(`INSERT INTO refresh_tokens (id, user_id, session_id, token_hash, expires_at, ip_address, user_agent, last_used_at) VALUES ($1, $2, $3, $4, NOW() + INTERVAL '1 hour', '203.0.113.16', 'qa-second-access', NOW())`, uuid.New(), userID, secondSessionID, uuid.NewString())
	secondSessionToken := tokenForSession(userID, secondSessionID, "user", "user")
	if response := requestAs(http.MethodGet, "/api/v1/user/me", secondSessionToken, nil); response.Code != http.StatusOK {
		t.Fatalf("second account session status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/admin/users/"+userID.String()+"/sessions/revoke", nil); response.Code != http.StatusOK {
		t.Fatalf("revoke all sessions status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(http.MethodGet, "/api/v1/user/me", secondSessionToken, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoke all left session active status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(http.MethodGet, "/api/v1/user/me", userToken, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoke all left legacy token active status=%d body=%s", response.Code, response.Body.String())
	}
}
