package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"syscall"
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

func (e execChecker) Place(ctx context.Context, t Target, flag string) Result {
	return e.run(ctx, "place", t, flag)
}

func (e execChecker) Check(ctx context.Context, t Target, flag string) Result {
	return e.run(ctx, "check", t, flag)
}

func (e execChecker) run(ctx context.Context, action string, t Target, flag string) Result {
	start := time.Now()
	task, _ := json.Marshal(checkerTask{Action: action, Host: t.Host, Port: t.Port, Flag: flag})

	out, err := runCheckerCommand(ctx, e.command, task)
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
