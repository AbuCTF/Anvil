package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestCORSAllowsRankRevalidationHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodOptions, "/api/v1/user/me/rank", nil)

	CORS()(ctx)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if allowed := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(allowed, "If-None-Match") {
		t.Fatalf("conditional request header is not allowed: %q", allowed)
	}
	if exposed := response.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(exposed, "ETag") {
		t.Fatalf("ETag response header is not exposed: %q", exposed)
	}
}

func TestParseAccessTokenAcceptsSupportedTokenTypes(t *testing.T) {
	cfg := testJWTConfig()
	tests := []struct {
		name   string
		claims Claims
	}{
		{
			name: "user",
			claims: Claims{
				UserID:    uuid.New(),
				TokenType: "user",
			},
		},
		{
			name: "team",
			claims: Claims{
				SessionID: uuid.New(),
				TokenType: "team",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signTestToken(t, jwt.SigningMethodHS256, tt.claims, cfg.JWT.Secret)
			if _, err := ParseAccessToken(token, cfg); err != nil {
				t.Fatalf("ParseAccessToken() returned error: %v", err)
			}
		})
	}
}

func TestParseAccessTokenRejectsUnsupportedAlgorithm(t *testing.T) {
	cfg := testJWTConfig()
	claims := Claims{UserID: uuid.New(), TokenType: "user"}
	token := signTestToken(t, jwt.SigningMethodHS384, claims, cfg.JWT.Secret)

	if _, err := ParseAccessToken(token, cfg); err == nil {
		t.Fatal("ParseAccessToken() accepted an HS384 token")
	}
}

func TestParseAccessTokenRejectsWrongIssuer(t *testing.T) {
	cfg := testJWTConfig()
	claims := Claims{
		UserID:    uuid.New(),
		TokenType: "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "another-service",
		},
	}
	token := signTestToken(t, jwt.SigningMethodHS256, claims, cfg.JWT.Secret)

	if _, err := ParseAccessToken(token, cfg); err == nil {
		t.Fatal("ParseAccessToken() accepted a token from another issuer")
	}
}

func TestParseAccessTokenRejectsInvalidTokenShape(t *testing.T) {
	cfg := testJWTConfig()
	tests := []struct {
		name   string
		claims Claims
	}{
		{name: "unknown type", claims: Claims{UserID: uuid.New(), TokenType: "admin"}},
		{name: "user without user ID", claims: Claims{TokenType: "user"}},
		{name: "team without session ID", claims: Claims{TokenType: "team"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signTestToken(t, jwt.SigningMethodHS256, tt.claims, cfg.JWT.Secret)
			if _, err := ParseAccessToken(token, cfg); err == nil {
				t.Fatal("ParseAccessToken() accepted invalid claims")
			}
		})
	}
}

func TestGetUserIDRejectsUnexpectedContextType(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", "not-a-uuid")
	if got := GetUserID(c); got != nil {
		t.Fatalf("GetUserID() = %v, want nil", got)
	}

	want := uuid.New()
	c.Set("user_id", want)
	got := GetUserID(c)
	if got == nil || *got != want {
		t.Fatalf("GetUserID() = %v, want %v", got, want)
	}
}

func TestHashAccessTokenIsStableAndDoesNotExposeToken(t *testing.T) {
	token := "header.payload.signature"
	first := HashAccessToken(token)
	second := HashAccessToken(token)
	if first != second {
		t.Fatalf("hash changed between calls: %q != %q", first, second)
	}
	if first == token || len(first) != 64 {
		t.Fatalf("unexpected access-token hash %q", first)
	}
	if first == HashAccessToken(token+"x") {
		t.Fatal("different bearer tokens produced the same test hash")
	}
}

func testJWTConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret",
			Issuer: "anvil-test",
		},
	}
}

func signTestToken(t *testing.T, method jwt.SigningMethod, claims Claims, secret string) string {
	t.Helper()
	if claims.Issuer == "" {
		claims.Issuer = "anvil-test"
	}
	claims.RegisteredClaims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Minute))
	token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}
