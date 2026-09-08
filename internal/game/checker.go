package game

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

// Target is a service endpoint on a team's vulnbox.
type Target struct {
	Host string
	Port int
}

// Result is a checker's verdict for one action.
type Result struct {
	Status  models.SLAStatus
	Latency time.Duration
	Message string
}

// Checker places and verifies flags on a service. The reference implementation
// shells out to an external executable over a JSON protocol (execChecker), so
// checkers can be written in any language.
type Checker interface {
	Place(ctx context.Context, t Target, flag string) Result
	Check(ctx context.Context, t Target, flag string) Result
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
	Status  string `json:"status"`
	Message string `json:"message"`
}

// execChecker invokes an external checker executable, one process per action.
type execChecker struct {
	command string
}

func (e execChecker) Place(ctx context.Context, t Target, flag string) Result {
	return e.run(ctx, "place", t, flag)
}

func (e execChecker) Check(ctx context.Context, t Target, flag string) Result {
	return e.run(ctx, "check", t, flag)
}

func (e execChecker) run(ctx context.Context, action string, t Target, flag string) Result {
	start := time.Now()
	task, _ := json.Marshal(checkerTask{Action: action, Host: t.Host, Port: t.Port, Flag: flag})

	cmd := exec.CommandContext(ctx, e.command)
	cmd.Stdin = bytes.NewReader(task)
	out, err := cmd.Output()
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
	return Result{Status: normalizeStatus(reply.Status), Latency: latency, Message: reply.Message}
}

func normalizeStatus(s string) models.SLAStatus {
	switch models.SLAStatus(s) {
	case models.SLAOk, models.SLADown, models.SLAFaulty, models.SLAFlagNotFound, models.SLARecovering:
		return models.SLAStatus(s)
	default:
		return models.SLAFaulty
	}
}
