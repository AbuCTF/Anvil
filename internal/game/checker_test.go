package game

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

func writeChecker(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "checker.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecCheckerOK(t *testing.T) {
	script := writeChecker(t, "#!/bin/sh\ncat >/dev/null\nprintf '{\"status\":\"OK\",\"message\":\"up\"}'\n")
	res := execChecker{command: script}.Place(context.Background(), Target{Host: "10.0.0.1", Port: 9001}, "H7CTF{x}")
	if res.Status != models.SLAOk {
		t.Fatalf("expected OK, got %s (%s)", res.Status, res.Message)
	}
}

func TestExecCheckerMalformed(t *testing.T) {
	script := writeChecker(t, "#!/bin/sh\ncat >/dev/null\nprintf 'not json'\n")
	res := execChecker{command: script}.Check(context.Background(), Target{}, "f")
	if res.Status != models.SLAFaulty {
		t.Fatalf("expected FAULTY, got %s", res.Status)
	}
}

func TestExecCheckerNonZeroExit(t *testing.T) {
	script := writeChecker(t, "#!/bin/sh\nexit 1\n")
	res := execChecker{command: script}.Check(context.Background(), Target{}, "f")
	if res.Status != models.SLADown {
		t.Fatalf("expected DOWN, got %s", res.Status)
	}
}

func TestExecCheckerTimeout(t *testing.T) {
	script := writeChecker(t, "#!/bin/sh\nsleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := execChecker{command: script}.Check(ctx, Target{}, "f")
	if res.Status != models.SLADown {
		t.Fatalf("expected DOWN on timeout, got %s", res.Status)
	}
}

func TestNormalizeStatus(t *testing.T) {
	if normalizeStatus("OK") != models.SLAOk {
		t.Fatal("OK should normalize to SLAOk")
	}
	if normalizeStatus("garbage") != models.SLAFaulty {
		t.Fatal("unknown status should normalize to FAULTY")
	}
}
