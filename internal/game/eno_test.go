package game

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

func enoServer(t *testing.T, reply func(enoTaskMessage) (int, string)) (*httptest.Server, chan enoTaskMessage) {
	t.Helper()
	seen := make(chan enoTaskMessage, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg enoTaskMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Errorf("decode task: %v", err)
		}
		seen <- msg
		code, body := reply(msg)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func TestEnoCheckerSendsProtocolTask(t *testing.T) {
	srv, seen := enoServer(t, func(enoTaskMessage) (int, string) {
		return 200, `{"result":"OK","message":null,"attackInfo":"user_k3"}`
	})
	task := Task{ID: 7, Method: MethodPutFlag, Variant: 1, Flag: "H7CTF{a}", ChainID: "flag_x",
		Tick: 4, RelatedTick: 4, TeamNumber: 3, TeamName: "bravo", Timeout: 15 * time.Second, RoundLength: time.Minute}
	res := checkerFor(srv.URL, srv.Client()).Run(context.Background(), Target{Host: "10.60.3.2", Port: 8080}, task)
	if res.Status != models.SLAOk || res.AttackInfo != "user_k3" {
		t.Fatalf("result = %+v", res)
	}
	msg := <-seen
	if msg.TaskID != 7 || msg.Method != "putflag" || msg.Address != "10.60.3.2" || msg.TeamID != 3 ||
		msg.CurrentRoundID != 4 || msg.RelatedRoundID != 4 || msg.VariantID != 1 || msg.Timeout != 15000 ||
		msg.RoundLength != 60000 || msg.TaskChainID != "flag_x" || msg.Flag == nil || *msg.Flag != "H7CTF{a}" {
		t.Fatalf("task message = %+v", msg)
	}
}

func TestEnoCheckerOmitsFlagForNoise(t *testing.T) {
	srv, seen := enoServer(t, func(enoTaskMessage) (int, string) {
		return 200, `{"result":"OK","message":null,"attackInfo":"must be ignored"}`
	})
	res := checkerFor(srv.URL, srv.Client()).Run(context.Background(), Target{}, Task{Method: MethodPutNoise, Flag: "x"})
	if msg := <-seen; msg.Flag != nil {
		t.Fatalf("noise task carried a flag: %q", *msg.Flag)
	}
	if res.AttackInfo != "" {
		t.Fatalf("attack info accepted from a non-putflag result: %q", res.AttackInfo)
	}
}

func TestReferenceAttackCheckerContract(t *testing.T) {
	const expectedFlag = "H7CTF{0123456789abcdef0123456789abcdef}"
	srv, seen := enoServer(t, func(enoTaskMessage) (int, string) {
		return 200, `{"result":"OK","message":null,"flag":"` + expectedFlag + `"}`
	})
	store := orangeStore{serviceID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), storeIndex: 1, waveVariant: 3, port: 7001}
	team := greenTeam{id: uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), number: 7, name: "bravo", vulnboxIP: "10.60.7.2"}
	flag := outbreakFlag{id: uuid.New(), value: expectedFlag, attackInfo: `{"account":"victim-7"}`}
	var opened []Task
	var closed []struct {
		id  int64
		res Result
	}
	bot := &outbreakBot{
		cfg: config.GameConfig{FlagPrefix: "H7CTF", TickInterval: 2 * time.Minute},
		taskOpen: func(_ uuid.UUID, _ uuid.UUID, task Task) (int64, error) {
			opened = append(opened, task)
			return int64(len(opened)), nil
		},
		taskClose: func(id int64, res Result) error {
			closed = append(closed, struct {
				id  int64
				res Result
			}{id: id, res: res})
			return nil
		},
	}
	checker := checkerFor(srv.URL, srv.Client())
	for range 2 {
		read, err := bot.callExploit(context.Background(), 23, store, team, flag, checker, 15*time.Second)
		if err != nil {
			t.Fatalf("reference attack: %v", err)
		}
		if read.outcome != "stolen" {
			t.Fatalf("reference attack outcome = %q, want stolen", read.outcome)
		}
	}

	digest := sha256.Sum256([]byte(expectedFlag))
	var taskIDs []int64
	for range 2 {
		msg := <-seen
		taskIDs = append(taskIDs, msg.TaskID)
		if msg.Method != MethodExploit || msg.Address != "10.60.7.2" || msg.Flag != nil ||
			msg.AttackInfo == nil || *msg.AttackInfo != `{"account":"victim-7"}` ||
			msg.FlagRegex != `H7CTF\{[0-9a-f]{32}\}` || msg.FlagHash != hex.EncodeToString(digest[:]) {
			t.Fatalf("reference attack task = %+v", msg)
		}
	}
	if taskIDs[0] == 0 || taskIDs[1] == 0 || taskIDs[0] == taskIDs[1] {
		t.Fatalf("reference attack task ids = %v, want unique nonzero values", taskIDs)
	}
	if len(closed) != 2 || closed[0].id != taskIDs[0] || closed[1].id != taskIDs[1] {
		t.Fatalf("closed task ids = %+v, want %v", closed, taskIDs)
	}
	for _, audit := range closed {
		if audit.res.Status != models.SLAOk || audit.res.Message != "" || audit.res.Flag != "" {
			t.Fatalf("persisted reference attack result = %+v", audit.res)
		}
	}
	if got := botOutcome(Result{Status: models.SLAOk, Message: expectedFlag}, expectedFlag); got != "blocked" {
		t.Fatalf("human message produced outcome %q, want blocked", got)
	}
}

func TestEnoCheckerResultMapping(t *testing.T) {
	for _, tt := range []struct {
		name   string
		code   int
		body   string
		method string
		want   models.SLAStatus
	}{
		{"mumble getflag", 200, `{"result":"MUMBLE","message":"flag missing"}`, MethodGetFlag, models.SLAFlagNotFound},
		{"mumble putflag", 200, `{"result":"MUMBLE","message":"bad login"}`, MethodPutFlag, models.SLAFaulty},
		{"mumble exploit", 200, `{"result":"MUMBLE","message":"reference attack failed"}`, MethodExploit, models.SLAFaulty},
		{"offline", 200, `{"result":"OFFLINE","message":"refused"}`, MethodHavoc, models.SLADown},
		{"internal error", 200, `{"result":"INTERNAL_ERROR","message":"trace"}`, MethodPutFlag, models.SLAError},
		{"unknown result", 200, `{"result":"MAYBE"}`, MethodPutFlag, models.SLAError},
		{"http error", 500, `boom`, MethodPutFlag, models.SLAError},
		{"garbage", 200, `not json`, MethodPutFlag, models.SLAError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := enoServer(t, func(enoTaskMessage) (int, string) { return tt.code, tt.body })
			res := checkerFor(srv.URL, srv.Client()).Run(context.Background(), Target{}, Task{Method: tt.method})
			if res.Status != tt.want {
				t.Fatalf("status = %s, want %s (%s)", res.Status, tt.want, res.Message)
			}
		})
	}
}

func TestEnoCheckerTimeoutIsOffline(t *testing.T) {
	srv, _ := enoServer(t, func(enoTaskMessage) (int, string) {
		time.Sleep(300 * time.Millisecond)
		return 200, `{"result":"OK"}`
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := checkerFor(srv.URL, srv.Client()).Run(ctx, Target{}, Task{Method: MethodGetFlag})
	if res.Status != models.SLADown {
		t.Fatalf("timeout status = %s, want DOWN", res.Status)
	}
}

func TestEnoCheckerUnreachableIsOrganizerFault(t *testing.T) {
	srv, _ := enoServer(t, func(enoTaskMessage) (int, string) { return 200, `{}` })
	url := srv.URL
	srv.Close()
	res := checkerFor(url, http.DefaultClient).Run(context.Background(), Target{}, Task{Method: MethodPutFlag})
	if res.Status != models.SLAError {
		t.Fatalf("unreachable checker status = %s, want ERROR", res.Status)
	}
}

func TestCheckerForPicksImplementation(t *testing.T) {
	if _, ok := checkerFor("https://checker:9000", nil).(enoChecker); !ok {
		t.Fatal("https ref should use the enowars checker")
	}
	if _, ok := checkerFor("/opt/checkers/notes", nil).(execChecker); !ok {
		t.Fatal("path ref should use the exec checker")
	}
}

func TestExecCheckerRejectsUnsupportedMethods(t *testing.T) {
	res := execChecker{command: "/bin/true"}.Run(context.Background(), Target{}, Task{Method: MethodHavoc})
	if res.Status != models.SLAError {
		t.Fatalf("havoc on an exec checker = %s, want ERROR", res.Status)
	}
}
