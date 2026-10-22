package game

import (
	"context"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

type gateCheckerFunc func(context.Context, Target, Task) Result

func (f gateCheckerFunc) Run(ctx context.Context, target Target, task Task) Result {
	return f(ctx, target, task)
}

func TestGateTaskSanitizesCheckerDiagnostics(t *testing.T) {
	gate := NewGateRunner(nil, 1, "H7CTF")
	checker := gateCheckerFunc(func(context.Context, Target, Task) Result {
		return Result{Status: models.SLAFaulty, Message: "GATE-CHECK-07: PRIVATE_HIDDEN_SENTINEL"}
	})
	result := gate.runGateTask(context.Background(), checker, Target{}, Task{}, time.Second)
	if result.Code != "GATE-CHECK-07" || result.Message != "Service response invalid" {
		t.Fatalf("gate diagnostic = code %q, message %q", result.Code, result.Message)
	}
}
