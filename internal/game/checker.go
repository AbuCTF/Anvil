package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

// Target is a service endpoint on a team's vulnbox.
type Target struct {
	Host string
	Port int
}

// checker task methods, named after the enowars checker protocol v2
const (
	MethodPutFlag  = "putflag"
	MethodGetFlag  = "getflag"
	MethodPutNoise = "putnoise"
	MethodGetNoise = "getnoise"
	MethodHavoc    = "havoc"
)

// Task is one checker action against one team's service.
type Task struct {
	ID          int64
	Method      string
	Variant     int
	Flag        string
	ChainID     string
	Tick        int
	RelatedTick int
	TeamNumber  int
	TeamName    string
	Timeout     time.Duration
	RoundLength time.Duration
}

// Result is a checker's verdict for one task.
type Result struct {
	Status     models.SLAStatus
	Latency    time.Duration
	Message    string
	AttackInfo string
}

// Checker runs tasks against a service. checker_ref picks the implementation: an
// http(s) url speaks the enowars protocol, anything else is a local executable.
type Checker interface {
	Run(ctx context.Context, t Target, task Task) Result
}

func checkerFor(ref string, client *http.Client) Checker {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return enoChecker{url: ref, client: client}
	}
	return execChecker{command: ref}
}

// checkerTask is written to an external checker's stdin.
type checkerTask struct {
	Action string `json:"action"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Flag   string `json:"flag"`
}

// checkerReply is read from an external checker's stdout.
type checkerReply struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	AttackInfo string `json:"attack_info"`
}

// execChecker invokes an external checker executable, one process per action. It
// only knows place/check, so services using it carry no noise or havoc variants.
type execChecker struct {
	command string
}

// runCheckerCommand gives every checker its own process group. CommandContext
// otherwise kills only the direct child on cancellation, leaving checker
// grandchildren alive with inherited stdout pipes and making a short timeout
// block until those descendants eventually exit.
func runCheckerCommand(ctx context.Context, command string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command)
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Output()
}

func (e execChecker) Run(ctx context.Context, t Target, task Task) Result {
	var action string
	switch task.Method {
	case MethodPutFlag:
		action = "place"
	case MethodGetFlag:
		action = "check"
	default:
		return Result{Status: models.SLAError, Message: "exec checker has no " + task.Method}
	}

	start := time.Now()
	input, _ := json.Marshal(checkerTask{Action: action, Host: t.Host, Port: t.Port, Flag: task.Flag})
	out, err := runCheckerCommand(ctx, e.command, input)
	latency := time.Since(start)

	if ctx.Err() != nil {
		return Result{Status: models.SLADown, Latency: latency, Message: "checker timed out"}
	}
	if err != nil {
		return Result{Status: models.SLADown, Latency: latency, Message: err.Error()}
	}

	var reply checkerReply
	if err := json.Unmarshal(bytes.TrimSpace(out), &reply); err != nil {
		return Result{Status: models.SLAFaulty, Latency: latency, Message: "malformed checker output"}
	}
	return Result{Status: normalizeStatus(reply.Status), Latency: latency, Message: reply.Message, AttackInfo: reply.AttackInfo}
}

func normalizeStatus(s string) models.SLAStatus {
	switch models.SLAStatus(s) {
	case models.SLAOk, models.SLADown, models.SLAFaulty, models.SLAFlagNotFound, models.SLARecovering, models.SLAError:
		return models.SLAStatus(s)
	default:
		return models.SLAFaulty
	}
}

// enoChecker speaks enowars checker protocol v2: POST a task, get a result back.
// https://github.com/enowars/specification/blob/main/checker_protocol.md
type enoChecker struct {
	url    string
	client *http.Client
}

type enoTaskMessage struct {
	TaskID         int64   `json:"taskId"`
	Method         string  `json:"method"`
	Address        string  `json:"address"`
	TeamID         int     `json:"teamId"`
	TeamName       string  `json:"teamName"`
	CurrentRoundID int     `json:"currentRoundId"`
	RelatedRoundID int     `json:"relatedRoundId"`
	Flag           *string `json:"flag"`
	VariantID      int     `json:"variantId"`
	Timeout        int64   `json:"timeout"`
	RoundLength    int64   `json:"roundLength"`
	TaskChainID    string  `json:"taskChainId"`
}

type enoResultMessage struct {
	Result     string  `json:"result"`
	Message    *string `json:"message"`
	AttackInfo *string `json:"attackInfo"`
}

func (e enoChecker) Run(ctx context.Context, t Target, task Task) Result {
	msg := enoTaskMessage{
		TaskID:         task.ID,
		Method:         task.Method,
		Address:        t.Host,
		TeamID:         task.TeamNumber,
		TeamName:       task.TeamName,
		CurrentRoundID: task.Tick,
		RelatedRoundID: task.RelatedTick,
		VariantID:      task.Variant,
		Timeout:        task.Timeout.Milliseconds(),
		RoundLength:    task.RoundLength.Milliseconds(),
		TaskChainID:    task.ChainID,
	}
	if task.Method == MethodPutFlag || task.Method == MethodGetFlag {
		flag := task.Flag
		msg.Flag = &flag
	}
	body, _ := json.Marshal(msg)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return Result{Status: models.SLAError, Message: "bad checker url"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		// the protocol treats a checker that misses its timeout as the service being
		// offline; a checker we cannot reach at all is our fault, not the team's
		if ctx.Err() != nil {
			return Result{Status: models.SLADown, Latency: latency, Message: "timeout"}
		}
		return Result{Status: models.SLAError, Latency: latency, Message: "checker unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		if ctx.Err() != nil {
			return Result{Status: models.SLADown, Latency: latency, Message: "timeout"}
		}
		return Result{Status: models.SLAError, Latency: latency, Message: "checker read: " + err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{Status: models.SLAError, Latency: latency, Message: "checker http " + resp.Status}
	}

	var res enoResultMessage
	if err := json.Unmarshal(raw, &res); err != nil {
		return Result{Status: models.SLAError, Latency: latency, Message: "malformed checker result"}
	}
	out := Result{Status: enoStatus(res.Result, task.Method), Latency: latency}
	if res.Message != nil {
		out.Message = *res.Message
	}
	if res.AttackInfo != nil && task.Method == MethodPutFlag {
		out.AttackInfo = *res.AttackInfo
	}
	return out
}

// enoStatus maps protocol results onto sla statuses. MUMBLE on a getflag means the
// flag is gone, which is worth telling the team apart from a broken feature.
func enoStatus(result, method string) models.SLAStatus {
	switch result {
	case "OK":
		return models.SLAOk
	case "OFFLINE":
		return models.SLADown
	case "MUMBLE":
		if method == MethodGetFlag {
			return models.SLAFlagNotFound
		}
		return models.SLAFaulty
	default:
		return models.SLAError
	}
}
