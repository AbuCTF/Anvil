//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// destructive: truncates vpn_configs in a *_test database.
func TestExternalVPNConfigLifecyclePersistsDesiredPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE vpn_peer_history, vpn_configs`); err != nil {
		t.Fatalf("truncate vpn configs: %v", err)
	}
	uid := uuid.New()
	teamID := uuid.New()
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO teams (id, name, join_code) VALUES ($1, $2, $3)`,
		teamID, "vpn-team-"+suffix, "vpn-join-"+suffix); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_teams (id, name, slug, team_number) VALUES ($1, $2, $3, 7)`,
		teamID, "vpn-team-"+suffix, "vpn-team-"+suffix); err != nil {
		t.Fatalf("insert game team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, $2, $3, 'user', 'active', $4)`,
		uid, "vpn"+suffix, "vpn"+suffix+"@example.test", teamID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM vpn_peer_history WHERE user_id IN (SELECT id FROM users WHERE team_id = $1)`, teamID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE team_id = $1`, teamID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id = $1`, teamID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID)
	}()

	cfg := &config.Config{
		Environment: "production",
		Secrets:     config.SecretsConfig{EncryptionKey: strings.Repeat("v", 32)},
		VPN: config.VPNConfig{
			Enabled:           true,
			Interface:         "wg0",
			ListenPort:        443,
			PublicEndpoint:    "vpn.h7tex.com",
			PublicKey:         "server-key",
			AddressRange:      "10.80.0.0/24",
			AllowedIPs:        "10.60.0.0/15",
			PeerControl:       "external",
			MTU:               1420,
			KeepaliveInterval: 8,
		},
	}
	service, err := vpn.NewService(cfg.VPN, db, zap.NewNop())
	if err != nil {
		t.Fatalf("new vpn service: %v", err)
	}
	handler := NewVPNHandler(cfg, db, service, zap.NewNop())
	request := func(requestUser uuid.UUID, handle gin.HandlerFunc) (int, VPNConfigResponse) {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/vpn/config", nil)
		ginContext, _ := gin.CreateTestContext(response)
		ginContext.Request = request
		ginContext.Set("user_id", requestUser)
		handle(ginContext)
		var payload VPNConfigResponse
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v\n%s", err, response.Body.String())
		}
		return response.Code, payload
	}
	unassignedUser := uuid.New()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'user', 'active')`,
		unassignedUser, "vpn"+suffix+"-unassigned", "vpn"+suffix+"-unassigned@example.test"); err != nil {
		t.Fatalf("insert unassigned user: %v", err)
	}
	defer func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, unassignedUser) }()
	if code, _ := request(unassignedUser, handler.GenerateConfig); code != http.StatusForbidden {
		t.Fatalf("unassigned user generate = %d, want 403", code)
	}

	invalidTeamID, invalidUser := uuid.New(), uuid.New()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO teams (id, name, join_code) VALUES ($1, $2, $3)`,
		invalidTeamID, "vpn-invalid-"+suffix, "vpn-invalid-join-"+suffix); err != nil {
		t.Fatalf("insert invalid team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_teams (id, name, slug, team_number) VALUES ($1, $2, $3, 16)`,
		invalidTeamID, "vpn-invalid-"+suffix, "vpn-invalid-"+suffix); err != nil {
		t.Fatalf("insert invalid game team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, $2, $3, 'user', 'active', $4)`,
		invalidUser, "vpn"+suffix+"-invalid", "vpn"+suffix+"-invalid@example.test", invalidTeamID); err != nil {
		t.Fatalf("insert invalid team user: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, invalidUser)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id = $1`, invalidTeamID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, invalidTeamID)
	}()
	if code, _ := request(invalidUser, handler.GenerateConfig); code != http.StatusConflict {
		t.Fatalf("invalid team generate = %d, want 409", code)
	}

	code, created := request(uid, handler.GenerateConfig)
	if code != http.StatusCreated || created.IPAddress == nil || created.PublicKey == nil || created.ConfigFile == nil {
		t.Fatalf("generate = %d %#v", code, created)
	}
	if !strings.Contains(*created.ConfigFile, "AllowedIPs = 10.60.0.0/15") {
		t.Fatalf("generated config has wrong routes:\n%s", *created.ConfigFile)
	}
	if *created.IPAddress != "10.80.7.200" {
		t.Fatalf("generated IP = %q, want team pool 10.80.7.200", *created.IPAddress)
	}

	var oldKey, oldIP string
	var storedPrivateKey string
	var storedPrivateKeyCiphertext []byte
	var active bool
	if err := db.Pool.QueryRow(ctx,
		`SELECT public_key, assigned_ip, private_key, private_key_ciphertext, is_active FROM vpn_configs WHERE user_id = $1`, uid,
	).Scan(&oldKey, &oldIP, &storedPrivateKey, &storedPrivateKeyCiphertext, &active); err != nil {
		t.Fatalf("read created desired peer: %v", err)
	}
	if !active || oldKey != *created.PublicKey || oldIP != *created.IPAddress || storedPrivateKey != "" || len(storedPrivateKeyCiphertext) == 0 {
		t.Fatalf("created desired peer = active:%v key:%q ip:%q plaintext:%q ciphertext:%d",
			active, oldKey, oldIP, storedPrivateKey, len(storedPrivateKeyCiphertext))
	}
	if _, err := db.Pool.Exec(ctx,
		`UPDATE vpn_configs SET private_key = 'legacy-private-key', private_key_ciphertext = NULL WHERE user_id = $1`, uid,
	); err != nil {
		t.Fatalf("seed legacy private key: %v", err)
	}
	code, legacy := request(uid, handler.GetConfig)
	if code != http.StatusOK || legacy.ConfigFile == nil || !strings.Contains(*legacy.ConfigFile, "PrivateKey = legacy-private-key") {
		t.Fatalf("legacy config read = %d %#v", code, legacy)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT private_key, private_key_ciphertext FROM vpn_configs WHERE user_id = $1`, uid,
	).Scan(&storedPrivateKey, &storedPrivateKeyCiphertext); err != nil {
		t.Fatalf("read migrated legacy key: %v", err)
	}
	if storedPrivateKey != "" || len(storedPrivateKeyCiphertext) == 0 {
		t.Fatalf("legacy key was not encrypted and cleared: plaintext=%q ciphertext=%d", storedPrivateKey, len(storedPrivateKeyCiphertext))
	}
	if _, err := db.Pool.Exec(ctx,
		`UPDATE vpn_configs SET last_handshake = NOW(), bytes_sent = 123, bytes_received = 456 WHERE user_id = $1`, uid,
	); err != nil {
		t.Fatalf("seed status counters: %v", err)
	}

	code, regenerated := request(uid, handler.RegenerateConfig)
	if code != http.StatusOK || regenerated.IPAddress == nil || regenerated.PublicKey == nil {
		t.Fatalf("regenerate = %d %#v", code, regenerated)
	}
	if *regenerated.PublicKey == oldKey || *regenerated.IPAddress == oldIP {
		t.Fatalf("regeneration reused identity: same-key:%v same-address:%v", *regenerated.PublicKey == oldKey, *regenerated.IPAddress == oldIP)
	}
	if *regenerated.IPAddress != "10.80.7.201" {
		t.Fatalf("regenerated IP = %q, want fresh team lease 10.80.7.201", *regenerated.IPAddress)
	}

	var currentKey, currentIP string
	var lastHandshake any
	var bytesSent, bytesReceived, rows int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT public_key, assigned_ip, last_handshake, bytes_sent, bytes_received,
		       COUNT(*) OVER ()
		FROM vpn_configs WHERE user_id = $1`, uid,
	).Scan(&currentKey, &currentIP, &lastHandshake, &bytesSent, &bytesReceived, &rows); err != nil {
		t.Fatalf("read regenerated desired peer: %v", err)
	}
	if currentKey != *regenerated.PublicKey || currentIP != *regenerated.IPAddress ||
		lastHandshake != nil || bytesSent != 0 || bytesReceived != 0 || rows != 1 {
		t.Fatalf("regenerated desired peer = key:%q ip:%q handshake:%v sent:%d received:%d rows:%d",
			currentKey, currentIP, lastHandshake, bytesSent, bytesReceived, rows)
	}

	code, _ = request(uid, handler.RevokeConfig)
	if code != http.StatusOK {
		t.Fatalf("revoke = %d, want 200", code)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT is_active, private_key, private_key_ciphertext FROM vpn_configs WHERE user_id = $1`, uid,
	).Scan(&active, &storedPrivateKey, &storedPrivateKeyCiphertext); err != nil {
		t.Fatalf("read revoked desired peer: %v", err)
	}
	if active || storedPrivateKey != "" || storedPrivateKeyCiphertext != nil {
		t.Fatalf("revoked desired peer = active:%v plaintext:%q ciphertext:%d", active, storedPrivateKey, len(storedPrivateKeyCiphertext))
	}

	code, reissued := request(uid, handler.GenerateConfig)
	if code != http.StatusCreated || reissued.IPAddress == nil || reissued.PublicKey == nil ||
		*reissued.IPAddress != "10.80.7.202" || *reissued.PublicKey == *regenerated.PublicKey {
		t.Fatalf("reissue = %d %#v, want a fresh identity on 10.80.7.202", code, reissued)
	}
	var actions []string
	rowsResult, err := db.Pool.Query(ctx,
		`SELECT action FROM vpn_peer_history WHERE user_id = $1 ORDER BY id`, uid)
	if err != nil {
		t.Fatalf("read peer history: %v", err)
	}
	defer rowsResult.Close()
	for rowsResult.Next() {
		var action string
		if err := rowsResult.Scan(&action); err != nil {
			t.Fatalf("scan peer history: %v", err)
		}
		actions = append(actions, action)
	}
	if strings.Join(actions, ",") != "issued,rotated,issued,revoked,issued" {
		t.Fatalf("peer history = %v", actions)
	}

	var wg sync.WaitGroup
	resultCodes := make(chan int, 37)
	for i := 0; i < 37; i++ {
		poolUser := uuid.New()
		if _, err := db.Pool.Exec(ctx,
			`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, $2, $3, 'user', 'active', $4)`,
			poolUser, "vpn"+suffix+"-"+strconv.Itoa(i), "vpn"+suffix+"-"+strconv.Itoa(i)+"@example.test", teamID); err != nil {
			t.Fatalf("insert pool user: %v", err)
		}
		wg.Add(1)
		go func(userID uuid.UUID) {
			defer wg.Done()
			status, _ := request(userID, handler.GenerateConfig)
			resultCodes <- status
		}(poolUser)
	}
	wg.Wait()
	close(resultCodes)
	createdCount := 0
	for status := range resultCodes {
		if status == http.StatusCreated {
			createdCount++
		} else {
			t.Fatalf("concurrent pool allocation returned %d", status)
		}
	}
	if createdCount != 37 {
		t.Fatalf("team pool created %d leases, want 37", createdCount)
	}
	overflowUser := uuid.New()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, $2, $3, 'user', 'active', $4)`,
		overflowUser, "vpn"+suffix+"-overflow", "vpn"+suffix+"-overflow@example.test", teamID); err != nil {
		t.Fatalf("insert overflow user: %v", err)
	}
	if status, _ := request(overflowUser, handler.GenerateConfig); status != http.StatusConflict {
		t.Fatalf("exhausted team pool returned %d, want 409", status)
	}
	var activeRows, distinctIPs int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT v.assigned_ip)
		FROM vpn_configs v JOIN users u ON u.id = v.user_id
		WHERE u.team_id = $1 AND v.is_active = TRUE`, teamID,
	).Scan(&activeRows, &distinctIPs); err != nil {
		t.Fatalf("count team pool: %v", err)
	}
	if activeRows != 38 || distinctIPs != 38 {
		t.Fatalf("team pool active/distinct = %d/%d, want 38/38", activeRows, distinctIPs)
	}
	if status, _ := request(uid, handler.RegenerateConfig); status != http.StatusConflict {
		t.Fatalf("rotation with exhausted historical pool returned %d, want 409", status)
	}
	if status, _ := request(uid, handler.RevokeConfig); status != http.StatusOK {
		t.Fatalf("revoke exhausted-pool lease returned %d, want 200", status)
	}
	if status, _ := request(overflowUser, handler.GenerateConfig); status != http.StatusConflict {
		t.Fatalf("revoked address was reused: generate returned %d, want 409", status)
	}
	var issued, distinctIssued int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT assigned_ip)
		FROM vpn_peer_history WHERE team_id = $1 AND action = 'issued'`, teamID,
	).Scan(&issued, &distinctIssued); err != nil {
		t.Fatalf("count issued identities: %v", err)
	}
	if issued != 40 || distinctIssued != 40 {
		t.Fatalf("issued/distinct historical addresses = %d/%d, want 40/40", issued, distinctIssued)
	}

	internal := NewGameInternalHandler(cfg, db, zap.NewNop())
	router := gin.New()
	router.GET("/api/v1/internal/arena/vpn/peers", RequireInternalToken("inventory-token"), internal.VPNPeers)
	callInventory := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/arena/vpn/peers", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		router.ServeHTTP(response, req)
		return response
	}
	if response := callInventory(""); response.Code != http.StatusNotFound {
		t.Fatalf("inventory without token = %d, want 404", response.Code)
	}
	if response := callInventory("wrong"); response.Code != http.StatusNotFound {
		t.Fatalf("inventory with wrong token = %d, want 404", response.Code)
	}
	response := callInventory("inventory-token")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "user_id") || strings.Contains(response.Body.String(), "private_key") {
		t.Fatalf("inventory response = %d %s", response.Code, response.Body.String())
	}
	var inventory struct {
		Peers []desiredVPNPeer `json:"peers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if len(inventory.Peers) != 37 || inventory.Peers[0].TeamID != teamID ||
		inventory.Peers[0].TeamNumber != 7 || inventory.Peers[0].Address != "10.80.7.203/32" {
		t.Fatalf("inventory peers = %#v", inventory.Peers)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE vpn_peer_history SET team_number = 16
		WHERE action = 'issued' AND public_key = (
			SELECT public_key FROM vpn_configs WHERE is_active = TRUE ORDER BY assigned_ip LIMIT 1
		)`); err != nil {
		t.Fatalf("corrupt desired peer attribution: %v", err)
	}
	if response := callInventory("inventory-token"); response.Code != http.StatusInternalServerError {
		t.Fatalf("invalid inventory = %d %s, want fail-closed 500", response.Code, response.Body.String())
	}
}
