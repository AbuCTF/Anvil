package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/google/uuid"
)

const testGraderSecret = "5f2b8c0e9a714d3e8b6a2c1f0e9d8c7b6a5f4e3d2c1b0a998877665544332211"

// contract vector, computed independently of gradedMAC.
func contractSignature(key []byte, ts, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ts + "\n" + nonce + "\n" + hex.EncodeToString(digest[:])))
	return hex.EncodeToString(m.Sum(nil))
}

func TestGradedSignature(t *testing.T) {
	body := []byte(`{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","score":0.5,"idempotency_key":"k1"}`)
	ts, nonce := "1790000000", "00112233445566778899aabbccddeeff"
	sig := contractSignature([]byte(testGraderSecret), ts, nonce, body)

	if got := gradedSignature(testGraderSecret, ts, nonce, body); got != sig {
		t.Fatalf("gradedSignature = %s, want %s", got, sig)
	}
	if !verifyGradedSignature(testGraderSecret, ts, nonce, body, sig) {
		t.Fatal("valid signature rejected")
	}
	if !verifyGradedSignature(testGraderSecret, ts, nonce, body, strings.ToUpper(sig)) {
		t.Fatal("upper-case hex signature rejected")
	}
	raw, _ := hex.DecodeString(testGraderSecret)
	if !verifyGradedSignature(testGraderSecret, ts, nonce, body, contractSignature(raw, ts, nonce, body)) {
		t.Fatal("signature keyed with the hex-decoded secret rejected")
	}

	tampered := []struct {
		name, ts, nonce, sig string
		body                 []byte
	}{
		{"body", ts, nonce, sig, []byte(strings.Replace(string(body), "0.5", "1", 1))},
		{"timestamp", "1790000001", nonce, sig, body},
		{"nonce", ts, "00112233445566778899aabbccddeef0", sig, body},
		{"signature", ts, nonce, sig[:63] + "0", body},
		{"truncated", ts, nonce, sig[:62], body},
		{"not hex", ts, nonce, strings.Repeat("zz", 32), body},
		{"empty", ts, nonce, "", body},
	}
	for _, tc := range tampered {
		if tc.name == "signature" && sig[63] == '0' {
			tc.sig = sig[:63] + "1"
		}
		if verifyGradedSignature(testGraderSecret, tc.ts, tc.nonce, tc.body, tc.sig) {
			t.Errorf("%s: tampered report accepted", tc.name)
		}
	}
	if verifyGradedSignature("another-secret", ts, nonce, body, sig) {
		t.Error("signature accepted under a different secret")
	}
}

// the per-instance key is hex(HMAC-SHA256(challenge secret, instance id)); a key
// for one instance must not sign for another.
func TestGraderKeyIsBoundToTheInstance(t *testing.T) {
	m := hmac.New(sha256.New, []byte(testGraderSecret))
	m.Write([]byte("3f9a0c1d2e4b5a67"))
	want := hex.EncodeToString(m.Sum(nil))
	keyA := graderKey(testGraderSecret, "3f9a0c1d2e4b5a67")
	if keyA != want || len(keyA) != 64 {
		t.Fatalf("graderKey = %s, want %s", keyA, want)
	}
	keyB := graderKey(testGraderSecret, "3f9a0c1d2e4b5a68")
	if keyA == keyB || graderKey("another-secret", "3f9a0c1d2e4b5a67") == keyA {
		t.Fatal("grader keys collide across instances or secrets")
	}
	body := []byte(`{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"e"}`)
	ts, nonce := "1790000000", strings.Repeat("cd", 16)
	sigA := gradedSignature(keyA, ts, nonce, body)
	if !verifyGradedSignature(keyA, ts, nonce, body, sigA) {
		t.Fatal("instance key rejected its own signature")
	}
	if verifyGradedSignature(keyB, ts, nonce, body, sigA) || verifyGradedSignature(testGraderSecret, ts, nonce, body, sigA) {
		t.Fatal("a signature under one instance's key verified under another key")
	}
}

func gradedTestHeaders(slug, ts, nonce, sig string) http.Header {
	return gradedInstanceHeaders(slug, "3f9a0c1d2e4b5a67", ts, nonce, sig)
}

func gradedInstanceHeaders(slug, instance, ts, nonce, sig string) http.Header {
	h := http.Header{}
	for k, v := range map[string]string{
		"X-Anvil-Challenge": slug, "X-Anvil-Instance": instance, "X-Anvil-Timestamp": ts,
		"X-Anvil-Nonce": nonce, "X-Anvil-Signature": sig,
	} {
		if v != "" {
			h.Set(k, v)
		}
	}
	return h
}

func TestCheckGradedHeaders(t *testing.T) {
	now := time.Unix(1790000000, 0)
	nonce := strings.Repeat("ab", 16)
	sig := strings.Repeat("0", 64)
	at := func(d time.Duration) string { return itoa(now.Add(d).Unix()) }

	cases := []struct {
		name       string
		header     http.Header
		wantStatus int
	}{
		{"ok", gradedTestHeaders("depth", at(0), nonce, sig), 0},
		{"ok at -119s", gradedTestHeaders("depth", at(-119*time.Second), nonce, sig), 0},
		{"ok at +119s", gradedTestHeaders("depth", at(119*time.Second), nonce, sig), 0},
		{"stale", gradedTestHeaders("depth", at(-121*time.Second), nonce, sig), http.StatusUnauthorized},
		{"future", gradedTestHeaders("depth", at(121*time.Second), nonce, sig), http.StatusUnauthorized},
		{"missing challenge", gradedTestHeaders("", at(0), nonce, sig), http.StatusUnauthorized},
		{"missing timestamp", gradedTestHeaders("depth", "", nonce, sig), http.StatusUnauthorized},
		{"missing nonce", gradedTestHeaders("depth", at(0), "", sig), http.StatusUnauthorized},
		{"missing signature", gradedTestHeaders("depth", at(0), nonce, ""), http.StatusUnauthorized},
		{"missing instance", gradedInstanceHeaders("depth", "", at(0), nonce, sig), http.StatusUnauthorized},
		{"instance too long", gradedInstanceHeaders("depth", strings.Repeat("i", 101), at(0), nonce, sig), http.StatusBadRequest},
		{"timestamp not integer", gradedTestHeaders("depth", "1790000000.5", nonce, sig), http.StatusBadRequest},
		{"timestamp millis", gradedTestHeaders("depth", itoa(now.UnixMilli()), nonce, sig), http.StatusUnauthorized},
		{"nonce 15 bytes", gradedTestHeaders("depth", at(0), strings.Repeat("ab", 15), sig), http.StatusBadRequest},
		{"nonce not hex", gradedTestHeaders("depth", at(0), strings.Repeat("zz", 16), sig), http.StatusBadRequest},
		{"nonce 64 bytes", gradedTestHeaders("depth", at(0), strings.Repeat("ab", 64), sig), 0},
		{"nonce too long", gradedTestHeaders("depth", at(0), strings.Repeat("ab", 64)+"a", sig), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, status, msg := checkGradedHeaders(tc.header, now)
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, msg, tc.wantStatus)
			}
		})
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestParseGradedReportBounds(t *testing.T) {
	team := `"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"ev-1"`
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"zero", `{` + team + `,"score":0,"idempotency_key":"a"}`, true},
		{"one", `{` + team + `,"score":1,"idempotency_key":"a"}`, true},
		{"fraction", `{` + team + `,"score":0.625,"idempotency_key":"a","raw":{"depth":5}}`, true},
		{"raw null", `{` + team + `,"score":0.5,"idempotency_key":"a","raw":null}`, true},
		{"negative", `{` + team + `,"score":-0.0001,"idempotency_key":"a"}`, false},
		{"above one", `{` + team + `,"score":1.0000001,"idempotency_key":"a"}`, false},
		{"overflow", `{` + team + `,"score":1e400,"idempotency_key":"a"}`, false},
		{"nan literal", `{` + team + `,"score":NaN,"idempotency_key":"a"}`, false},
		{"string score", `{` + team + `,"score":"0.5","idempotency_key":"a"}`, false},
		{"missing score", `{` + team + `,"idempotency_key":"a"}`, false},
		{"null score", `{` + team + `,"score":null,"idempotency_key":"a"}`, false},
		{"bad team", `{"team_id":"team-7","score":0.5,"idempotency_key":"a"}`, false},
		{"missing team", `{"score":0.5,"idempotency_key":"a"}`, false},
		{"missing key", `{` + team + `,"score":0.5}`, false},
		{"key 64", `{` + team + `,"score":0.5,"idempotency_key":"` + strings.Repeat("k", 64) + `"}`, true},
		{"key 64 runes", `{` + team + `,"score":0.5,"idempotency_key":"` + strings.Repeat("é", 64) + `"}`, true},
		{"key 65", `{` + team + `,"score":0.5,"idempotency_key":"` + strings.Repeat("k", 65) + `"}`, false},
		{"key nul", `{` + team + `,"score":0.5,"idempotency_key":"a\u0000b"}`, false},
		{"raw array", `{` + team + `,"score":0.5,"idempotency_key":"a","raw":[1,2]}`, false},
		{"raw too big", `{` + team + `,"score":0.5,"idempotency_key":"a","raw":{"x":"` + strings.Repeat("x", gradedMaxRaw) + `"}}`, false},
		{"not an object", `[1,2,3]`, false},
		{"empty", ``, false},
		{"missing eval", `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","score":0.5,"idempotency_key":"a"}`, false},
		{"eval 65", `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"` + strings.Repeat("e", 65) + `","score":0.5,"idempotency_key":"a"}`, false},
		{"status ok", `{` + team + `,"status":"ok","score":0.5,"idempotency_key":"a"}`, true},
		{"infra error without score", `{` + team + `,"status":"infra_error","idempotency_key":"a"}`, true},
		{"infra error ignores score", `{` + team + `,"status":"infra_error","score":7,"idempotency_key":"a"}`, true},
		{"unknown status", `{` + team + `,"status":"crashed","score":0.5,"idempotency_key":"a"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, msg := parseGradedReport([]byte(tc.body))
			if tc.ok && msg != "" {
				t.Fatalf("rejected: %s", msg)
			}
			if !tc.ok && msg == "" {
				t.Fatalf("accepted: %+v", rep)
			}
		})
	}

	rep, msg := parseGradedReport([]byte(`{` + team + `,"score":0.75,"idempotency_key":"run-9","raw":{"depth":7}}`))
	if msg != "" || rep.score != 0.75 || rep.key != "run-9" || rep.evalID != "ev-1" || rep.status != "ok" ||
		rep.teamID.String() != "7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70" || string(rep.raw) != `{"depth":7}` {
		t.Fatalf("parsed report = %+v (%s)", rep, msg)
	}
}

func TestParseGradedEvaluate(t *testing.T) {
	ok := `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"sub-42"}`
	in, msg := parseGradedEvaluate([]byte(ok))
	if msg != "" || in.evalID != "sub-42" || in.teamID.String() != "7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70" {
		t.Fatalf("parsed = %+v (%s)", in, msg)
	}
	for name, body := range map[string]string{
		// a signed report body must never pass as an evaluate request
		"report body":  `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"sub-42","score":1,"idempotency_key":"k"}`,
		"trailing":     ok + ok,
		"missing eval": `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70"}`,
		"long eval":    `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"` + strings.Repeat("e", 65) + `"}`,
		"bad team":     `{"team_id":"x","eval_id":"sub-42"}`,
		"array":        `[]`,
	} {
		if _, msg := parseGradedEvaluate([]byte(body)); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGradedEvalCost(t *testing.T) {
	cfg := shippingEconomy() // launch costs 50/100/200/250
	for k := 1; k <= 3; k++ {
		if got := gradedEvalCost(cfg, "insane", k); got != 0 {
			t.Fatalf("evaluation %d costs %v, want free", k, got)
		}
	}
	// round(0.2 x launchCost x 1.5^(k-4))
	want := map[int]float64{4: 50, 5: 75, 6: 113, 7: 169, 8: 253}
	for k, w := range want {
		if got := gradedEvalCost(cfg, "insane", k); got != w {
			t.Errorf("insane evaluation %d = %v, want %v", k, got, w)
		}
	}
	if got := gradedEvalCost(cfg, "hard", 4); got != 40 {
		t.Errorf("hard evaluation 4 = %v, want 40", got)
	}
}

func TestGradedPoints(t *testing.T) {
	cases := []struct {
		best float64
		base int
		want int
	}{
		{0, 500, 0}, {1, 500, 500}, {0.5, 500, 250}, {0.333, 100, 33}, {0.335, 100, 34}, {0.999, 1000, 999},
	}
	for _, tc := range cases {
		if got := gradedPoints(tc.best, tc.base); got != tc.want {
			t.Errorf("gradedPoints(%v, %d) = %d, want %d", tc.best, tc.base, got, tc.want)
		}
	}
	// deltas sum to the final award no matter how the best climbs
	base, prev, total := 450, 0.0, 0
	for _, best := range []float64{0.1, 0.1004, 0.37, 0.371, 0.9, 1} {
		total += gradedPoints(best, base) - gradedPoints(prev, base)
		prev = best
	}
	if total != base {
		t.Fatalf("summed deltas = %d, want %d", total, base)
	}
}

func TestGradedLimiter(t *testing.T) {
	l := newGradedLimiter(60, time.Minute)
	start := time.Unix(1790000000, 0)
	for i := 0; i < 60; i++ {
		if ok, _ := l.allow("chal|team-a", start.Add(time.Duration(i)*time.Second/2)); !ok {
			t.Fatalf("report %d rejected under the limit", i+1)
		}
	}
	ok, wait := l.allow("chal|team-a", start.Add(40*time.Second))
	if ok {
		t.Fatal("61st report in the window accepted")
	}
	if wait <= 0 || wait > 20*time.Second {
		t.Fatalf("retry after = %s, want the rest of the window (20s)", wait)
	}
	if ok, _ := l.allow("chal|team-b", start.Add(40*time.Second)); !ok {
		t.Fatal("another team was limited by team-a's reports")
	}
	if ok, _ := l.allow("chal|team-a", start.Add(time.Minute)); !ok {
		t.Fatal("report rejected after the window rolled over")
	}
}

func TestGradedSecretNeverInPlayerResponses(t *testing.T) {
	for _, v := range []any{
		ChallengeListResponse{}, ChallengeDetailResponse{}, InstanceResponse{},
		gradedRaceResponse{}, gradedRaceEntry{}, gradedOwnBest{}, gradedEvalState{}, gradedEvaluateResponse{},
	} {
		assertNoSecretField(t, reflect.TypeOf(v))
	}
}

func assertNoSecretField(t *testing.T, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
		if strings.Contains(name, "secret") {
			t.Errorf("%s.%s can carry a secret into a player response", typ.Name(), f.Name)
		}
		if f.Type.PkgPath() == "" || strings.HasPrefix(f.Type.PkgPath(), "github.com/anvil-lab/") {
			assertNoSecretField(t, f.Type)
		}
	}
}

func TestGraderSecretOnlyReachesRolesThatAskForIt(t *testing.T) {
	subst := map[string]string{
		"GRADER_SECRET": testGraderSecret,
		"GRADER_URL":    "https://ctf.example/api/v1/graded/report",
		"ANVIL_TEAM_ID": "7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70",
	}
	web := resolveEnvPlaceholders(map[string]string{"GRADER": "http://grader:9000", "MODE": "prod"}, subst)
	for k, v := range web {
		if strings.Contains(v, testGraderSecret) {
			t.Fatalf("player role env %s received the grader secret", k)
		}
	}
	grader := resolveEnvPlaceholders(map[string]string{
		"SECRET": "${GRADER_SECRET}", "URL": "${GRADER_URL}", "TEAM": "${ANVIL_TEAM_ID}",
	}, subst)
	if grader["SECRET"] != testGraderSecret || grader["URL"] != subst["GRADER_URL"] || grader["TEAM"] != subst["ANVIL_TEAM_ID"] {
		t.Fatalf("grader env = %v", grader)
	}
	// a flag challenge's subst has no GRADER_SECRET: the placeholder stays unresolved
	flagOnly := resolveEnvPlaceholders(map[string]string{"SECRET": "${GRADER_SECRET}"}, map[string]string{"FLAG": "x"})
	if flagOnly["SECRET"] != "${GRADER_SECRET}" {
		t.Fatalf("unexpected substitution: %v", flagOnly)
	}

	if !referencesEnvPlaceholder(map[string]string{"K": "prefix-${GRADER_SECRET:-x}"}, "GRADER_SECRET") {
		t.Fatal("defaulted placeholder not detected")
	}
	if referencesEnvPlaceholder(map[string]string{"K": "$GRADER_SECRET", "L": "${GRADER_SECRET_X}"}, "GRADER_SECRET") {
		t.Fatal("non-placeholder text detected as a reference")
	}
}

func TestPublicRoleCannotRequestGraderSecret(t *testing.T) {
	h := &InstanceHandler{containerSvc: &container.Service{}}
	spec := func(publicEnv string) instanceChallenge {
		return instanceChallenge{
			ID: "c", ResourceType: "docker", ExposedPorts: []byte(`[]`),
			ContainerSpec: []byte(`[
				{"name":"web","image":"web","public":true,"env":{"X":"` + publicEnv + `"}},
				{"name":"grader","image":"grader","env":{"GRADER_SECRET":"${GRADER_SECRET}"}}
			]`),
		}
	}
	if _, opErr := h.prepareProvisionPlan(t.Context(), nil, spec("${GRADER_SECRET}")); opErr == nil || opErr.status != http.StatusInternalServerError {
		t.Fatalf("public role asking for GRADER_SECRET was provisioned: %+v", opErr)
	}
	if _, opErr := h.prepareProvisionPlan(t.Context(), nil, spec("http://grader:9000")); opErr != nil {
		t.Fatalf("internal grader role rejected: %+v", opErr.body)
	}
}

func TestGraderTeamID(t *testing.T) {
	uid := uuid.MustParse("0f6c4a8e-0c2b-4a51-9d7e-3b1f2a6c9e11")
	if got := graderTeamID("7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70", uid); got != "7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70" {
		t.Fatalf("team owner -> %q", got)
	}
	if got := graderTeamID(uid.String(), uid); got != "" {
		t.Fatalf("teamless owner -> %q, want empty", got)
	}
}
