package game

// gate.go — internal gate API: run a full checker suite against a staging copy.
// Called by the model-C deploy pipeline to verify a team's patched image before
// promotion to production. Gate flags use the real H7CTF{} format but are never
// written to game_flags and can never be submitted.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// GateTaskResult is one checker call's outcome.
type GateTaskResult struct {
	Round     int              `json:"round"`
	Method    string           `json:"method"`
	Variant   int              `json:"variant"`
	Status    models.SLAStatus `json:"status"`
	Message   string           `json:"message"`
	LatencyMs int64            `json:"latency_ms"`
}

// GateResult is the response returned to the deploy pipeline.
type GateResult struct {
	OK         bool             `json:"ok"`
	Service    string           `json:"service"`
	Address    string           `json:"address"`
	DurationMs int64            `json:"duration_ms"`
	Verdict    models.SLAStatus `json:"verdict"`
	Tasks      []GateTaskResult `json:"tasks"`
}

// GateRunner runs full checker suites for the model-C deploy gate. It is safe
// for concurrent use; a semaphore bounds total in-flight checker calls across
// all concurrent gate runs. This pool is intentionally separate from the live
// per-service dispatch pools so a gate call never queues behind a live task.
type GateRunner struct {
	db         *database.DB
	client     *http.Client
	sem        chan struct{}
	flagPrefix string
}

// NewGateRunner creates a GateRunner. concurrency caps the total checker tasks
// across all concurrent gate runs.
func NewGateRunner(db *database.DB, concurrency int, flagPrefix string) *GateRunner {
	if concurrency < 1 {
		concurrency = 16
	}
	if flagPrefix == "" {
		flagPrefix = "H7CTF"
	}
	return &GateRunner{
		db:         db,
		flagPrefix: flagPrefix,
		sem:        make(chan struct{}, concurrency),
		client: &http.Client{Transport: &http.Transport{
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     30 * time.Second,
		}},
	}
}

type gateServiceSpec struct {
	id            uuid.UUID
	checkerRef    string
	port          int
	flagVariants  int
	noiseVariants int
	havocVariants int
}

func (g *GateRunner) lookupService(ctx context.Context, slug string) (gateServiceSpec, error) {
	var s gateServiceSpec
	err := g.db.Pool.QueryRow(ctx,
		`SELECT id, checker_ref, COALESCE(port, 0), GREATEST(flag_stores, 1), COALESCE(noise_variants, 0), COALESCE(havoc_variants, 0)
		 FROM game_services WHERE slug = $1`, slug,
	).Scan(&s.id, &s.checkerRef, &s.port, &s.flagVariants, &s.noiseVariants, &s.havocVariants)
	if err != nil {
		return gateServiceSpec{}, fmt.Errorf("service %q not found: %w", slug, err)
	}
	if s.checkerRef == "" {
		return gateServiceSpec{}, fmt.Errorf("service %q has no checker_ref", slug)
	}
	if s.port == 0 {
		return gateServiceSpec{}, fmt.Errorf("service %q has no port configured", slug)
	}
	return s, nil
}

// RunGate runs rounds full checker suites (putflag, getflag, putnoise, getnoise,
// havoc for every variant) against address using the service's configured port.
// Flags use the real H7CTF{} format but are never written to game_flags.
// timeout is per-task.
func (g *GateRunner) RunGate(ctx context.Context, slug, address string, rounds int, taskTimeout time.Duration) (GateResult, error) {
	start := time.Now()
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 3 {
		rounds = 3
	}
	if taskTimeout <= 0 || taskTimeout > 30*time.Second {
		taskTimeout = 30 * time.Second
	}

	svc, err := g.lookupService(ctx, slug)
	if err != nil {
		return GateResult{}, err
	}

	checker := checkerFor(svc.checkerRef, g.client)
	target := Target{Host: address, Port: svc.port}

	var (
		mu    sync.Mutex
		tasks []GateTaskResult
	)

	add := func(r GateTaskResult) {
		mu.Lock()
		tasks = append(tasks, r)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for round := 1; round <= rounds; round++ {
		// flag variants: putflag then getflag
		for v := 0; v < svc.flagVariants; v++ {
			flag := gateFlag(g.flagPrefix, round, v)
			chainID := fmt.Sprintf("gate_s%s_r%d_i%d", svc.id, round, v)
			baseTask := Task{
				ID:          0,
				Tick:        round,
				RelatedTick: round,
				TeamNumber:  0,
				TeamName:    "gate",
				Timeout:     taskTimeout,
				RoundLength: taskTimeout * 4,
				ChainID:     chainID,
			}

			wg.Add(1)
			go func(round, v int, flag, chainID string, base Task) {
				defer wg.Done()

				put := base
				put.Method, put.Variant, put.Flag = MethodPutFlag, v, flag
				pr := g.runGateTask(ctx, checker, target, put, taskTimeout)
				add(GateTaskResult{Round: round, Method: MethodPutFlag, Variant: v,
					Status:    pr.Status,
					Message:   pr.Message,
					LatencyMs: pr.Latency.Milliseconds(),
				})
				if pr.Status != models.SLAOk {
					return
				}

				get := base
				get.Method, get.Variant, get.Flag = MethodGetFlag, v, flag
				gr := g.runGateTask(ctx, checker, target, get, taskTimeout)
				add(GateTaskResult{Round: round, Method: MethodGetFlag, Variant: v,
					Status:    gr.Status,
					Message:   gr.Message,
					LatencyMs: gr.Latency.Milliseconds(),
				})
			}(round, v, flag, chainID, baseTask)
		}

		// noise variants: putnoise then getnoise
		for v := 0; v < svc.noiseVariants; v++ {
			chainID := fmt.Sprintf("gate_noise_s%s_r%d_i%d", svc.id, round, v)
			base := Task{
				Tick:        round,
				RelatedTick: round,
				TeamNumber:  0,
				TeamName:    "gate",
				Timeout:     taskTimeout,
				RoundLength: taskTimeout * 4,
				ChainID:     chainID,
			}
			wg.Add(1)
			go func(round, v int, base Task) {
				defer wg.Done()
				put := base
				put.Method, put.Variant = MethodPutNoise, v
				pr := g.runGateTask(ctx, checker, target, put, taskTimeout)
				add(GateTaskResult{Round: round, Method: MethodPutNoise, Variant: v,
					Status: pr.Status, Message: pr.Message, LatencyMs: pr.Latency.Milliseconds()})
				if pr.Status != models.SLAOk {
					return
				}
				get := base
				get.Method, get.Variant = MethodGetNoise, v
				gr := g.runGateTask(ctx, checker, target, get, taskTimeout)
				add(GateTaskResult{Round: round, Method: MethodGetNoise, Variant: v,
					Status: gr.Status, Message: gr.Message, LatencyMs: gr.Latency.Milliseconds()})
			}(round, v, base)
		}

		// havoc variants
		for v := 0; v < svc.havocVariants; v++ {
			chainID := fmt.Sprintf("gate_havoc_s%s_r%d_i%d", svc.id, round, v)
			base := Task{
				Tick:        round,
				RelatedTick: round,
				TeamNumber:  0,
				TeamName:    "gate",
				Timeout:     taskTimeout,
				RoundLength: taskTimeout * 4,
				ChainID:     chainID,
			}
			wg.Add(1)
			go func(round, v int, base Task) {
				defer wg.Done()
				t := base
				t.Method, t.Variant = MethodHavoc, v
				hr := g.runGateTask(ctx, checker, target, t, taskTimeout)
				add(GateTaskResult{Round: round, Method: MethodHavoc, Variant: v,
					Status: hr.Status, Message: hr.Message, LatencyMs: hr.Latency.Milliseconds()})
			}(round, v, base)
		}
	}
	wg.Wait()

	verdict := models.SLAOk
	ok := true
	for _, t := range tasks {
		if severity(t.Status) > severity(verdict) {
			verdict = t.Status
		}
		if t.Status != models.SLAOk {
			ok = false
		}
	}

	return GateResult{
		OK:         ok,
		Service:    slug,
		Address:    address,
		DurationMs: time.Since(start).Milliseconds(),
		Verdict:    verdict,
		Tasks:      tasks,
	}, nil
}

func (g *GateRunner) runGateTask(ctx context.Context, checker Checker, target Target, task Task, timeout time.Duration) Result {
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-ctx.Done():
		return Result{Status: models.SLAError, Message: "gate cancelled"}
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return sanitizeCheckerResult(checker.Run(tctx, target, task))
}

// gateFlag generates a valid-format flag (H7CTF{[0-9a-f]{32}}) that is never written
// to game_flags. Checkers store and retrieve it by chainID, so any stable value per
// (round, variant) would work; using the real format means checkers that validate
// format (they shouldn't, but some do) still accept it.
func gateFlag(flagPrefix string, round, variant int) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// deterministic fallback: should never happen
		for i := range b {
			b[i] = byte(round*13 + variant*7 + i)
		}
	}
	enc := hex.EncodeToString(b)
	return fmt.Sprintf("%s{%s}", flagPrefix, enc)
}
