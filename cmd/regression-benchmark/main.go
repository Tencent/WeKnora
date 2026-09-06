// Command regression-benchmark runs Benchmark v1.1 against the currently
// checked-out code and writes the resulting unified BenchmarkResult as JSON.
//
// It is a thin driver over the existing EvaluationService / BenchmarkResultService
// runtime (resolved from the same DI container the server uses) — it does not
// re-implement benchmarking, metric calculation, or retrieval. It triggers an
// evaluation, polls it to a terminal state, then serializes the unified result
// so the regression comparator can compare it against the frozen baseline.
//
// This command needs a real runtime: a database (DB_* / RETRIEVE_DRIVER) and
// model provider credentials (see builtin_models.yaml / env). Exit code:
//
//	0 = benchmark completed and the current result was written
//	1 = benchmark failed, timed out, or the result could not be written
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	exitOK  = 0
	exitErr = 1
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("regression-benchmark", flag.ContinueOnError)
	fs.SetOutput(stderr)
	output := fs.String("output", "artifacts/regression/current.json", "path to write the current BenchmarkResult JSON")
	profilePath := fs.String("profile", "", "final benchmark profile JSON (enables strict preflight and final artifacts)")
	outputDir := fs.String("output-dir", "artifacts/rhino_2026_final/benchmark", "directory for final result.json and result.md")
	preflightOnly := fs.Bool("preflight-only", false, "validate final benchmark prerequisites without starting evaluation")
	backendURL := fs.String("backend-url", "http://127.0.0.1:8080", "running backend base URL checked by final preflight")
	dataset := fs.String("dataset", "benchmark_v1", "dataset ID to evaluate (benchmark_v1)")
	tenant := fs.Uint64("tenant", 10000, "tenant ID under which the evaluation runs")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum time to wait for the benchmark run")
	poll := fs.Duration("poll-interval", 5*time.Second, "status polling interval")
	if err := fs.Parse(args); err != nil {
		return exitErr
	}

	ctx := benchmarkContext(*tenant)
	var profile finalProfile
	var selected selectedModels
	var commitSHA string
	if *profilePath != "" {
		var err error
		profile, err = loadFinalProfile(*profilePath)
		if err != nil {
			fmt.Fprintf(stderr, "benchmark-v1 preflight: %v\n", err)
			return exitErr
		}
		if profile.BenchmarkID != *dataset {
			fmt.Fprintf(stderr, "benchmark-v1 preflight: dataset flag %q does not match profile benchmark_id %q\n", *dataset, profile.BenchmarkID)
			return exitErr
		}
		if err := checkOutputWritable(filepath.Join(*outputDir, "result.json")); err != nil {
			fmt.Fprintf(stderr, "benchmark-v1 preflight: %v\n", err)
			return exitErr
		}
		preflightCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = checkBackend(preflightCtx, *backendURL)
		if err != nil {
			cancel()
			fmt.Fprintf(stderr, "benchmark-v1 preflight: %v\n", err)
			return exitErr
		}
		env, err := collectPreflightEnvironment(preflightCtx, *tenant)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "benchmark-v1 preflight: %v\n", err)
			return exitErr
		}
		selected, err = validatePreflight(profile, env)
		if err != nil {
			fmt.Fprintf(stderr, "benchmark-v1 preflight: %v\n", err)
			return exitErr
		}
		commitSHA = env.CommitSHA
		fmt.Fprintf(stdout, "benchmark-v1 preflight PASS: commit=%s dataset=%s models=%s,%s,%s cache=off worker_limit=%d\n", commitSHA, profile.Dataset.SemanticSHA256, profile.Models.Embedding.Name, profile.Models.Chat.Name, profile.Models.Rerank.Name, profile.Runtime.WorkerLimit)
		if *preflightOnly {
			fmt.Fprintln(stdout, "preflight-only: evaluation was not started; no provider API was called")
			return exitOK
		}
	}

	// Resolve the exact runtime the server uses. BuildContainer wires the full
	// stack (DB, migrations, builtin model seeding, retrieval engines) so the
	// evaluation runs against the same environment as a production benchmark.
	var (
		result   *types.BenchmarkResult
		benchErr error
	)
	c := container.BuildContainer(runtime.GetContainer())
	if err := c.Invoke(func(
		evalSvc interfaces.EvaluationService,
		resultSvc interfaces.BenchmarkResultService,
	) {
		if *profilePath == "" {
			result, benchErr = runBenchmark(ctx, evalSvc, resultSvc, *dataset, *timeout, *poll)
		} else {
			result, benchErr = runBenchmarkWithModels(ctx, evalSvc, resultSvc, *dataset, selected.ChatID, selected.RerankID, *timeout, *poll)
		}
	}); err != nil {
		fmt.Fprintf(stderr, "regression-benchmark: resolve runtime: %v\n", err)
		return exitErr
	}
	if benchErr != nil {
		fmt.Fprintf(stderr, "regression-benchmark: %v\n", benchErr)
		return exitErr
	}

	if *profilePath != "" {
		artifact, err := buildFinalArtifact(profile, commitSHA, time.Now(), result)
		if err == nil {
			err = writeFinalArtifacts(*outputDir, artifact)
		}
		if err != nil {
			fmt.Fprintf(stderr, "benchmark-v1: %v\n", err)
			return exitErr
		}
		fmt.Fprintf(stdout, "task_id=%s evaluation_run_id=%s benchmark_version=%s written=%s\n", result.Run.TaskID, result.Run.EvaluationRunID, result.BenchmarkVersion, *outputDir)
		return exitOK
	}

	if err := writeResult(*output, result); err != nil {
		fmt.Fprintf(stderr, "regression-benchmark: %v\n", err)
		return exitErr
	}

	fmt.Fprintf(stdout, "task_id=%s evaluation_run_id=%s benchmark_version=%s written=%s\n",
		result.Run.TaskID, result.Run.EvaluationRunID, result.BenchmarkVersion, *output)
	return exitOK
}

// benchmarkContext builds the workspace context the internal benchmark driver
// runs under. The HTTP auth middleware normally injects both TenantID and
// TenantInfo; this CLI bypasses the middleware, so it must provide both or
// KnowledgeBaseService.applyAndValidateStorageBackend fails with "workspace
// context missing". TenantInfo is a minimal Tenant{ID} stub — the storage
// backend service hydrates the remaining storage config from the persisted
// workspace row when one exists. It does not fabricate a JWT/User identity or
// weaken any authorization check.
func benchmarkContext(tenantID uint64) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: tenantID})
}

// runBenchmark triggers a benchmark evaluation and blocks until it reaches a
// terminal state, then returns the unified result. It is separated from runCLI
// so the trigger/poll/serialize logic can be tested with stub services and no
// real model API.
func runBenchmark(
	ctx context.Context,
	evalSvc interfaces.EvaluationService,
	resultSvc interfaces.BenchmarkResultService,
	datasetID string,
	timeout, poll time.Duration,
) (*types.BenchmarkResult, error) {
	return runBenchmarkWithModels(ctx, evalSvc, resultSvc, datasetID, "", "", timeout, poll)
}

func runBenchmarkWithModels(
	ctx context.Context,
	evalSvc interfaces.EvaluationService,
	resultSvc interfaces.BenchmarkResultService,
	datasetID, chatModelID, rerankModelID string,
	timeout, poll time.Duration,
) (*types.BenchmarkResult, error) {
	detail, err := evalSvc.Evaluation(ctx, datasetID, "", chatModelID, rerankModelID)
	if err != nil {
		return nil, fmt.Errorf("start benchmark evaluation: %w", err)
	}
	if detail == nil || detail.Task == nil || detail.Task.ID == "" {
		return nil, fmt.Errorf("start benchmark evaluation: empty task identifier")
	}
	taskID := detail.Task.ID

	deadline := time.Now().Add(timeout)
	for {
		status, err := pollStatus(ctx, evalSvc, taskID)
		if err != nil {
			return nil, err
		}
		switch status {
		case types.EvaluationStatueSuccess:
			result, err := resultSvc.GetBenchmarkResult(ctx, taskID)
			if err != nil {
				return nil, fmt.Errorf("fetch benchmark result: %w", err)
			}
			if result == nil {
				// A successful run must always yield a result. A nil result here
				// would otherwise panic downstream on result.Run.TaskID; fail
				// closed instead.
				return nil, fmt.Errorf("fetch benchmark result: empty result for task %s", taskID)
			}
			return result, nil
		case types.EvaluationStatueFailed:
			return nil, fmt.Errorf("benchmark evaluation failed (task %s)", taskID)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("benchmark evaluation timed out after %s (task %s)", timeout, taskID)
		}
		time.Sleep(poll)
	}
}

func pollStatus(ctx context.Context, evalSvc interfaces.EvaluationService, taskID string) (types.EvaluationStatue, error) {
	detail, err := evalSvc.EvaluationResult(ctx, taskID)
	if err != nil {
		return types.EvaluationStatuePending, fmt.Errorf("poll benchmark evaluation: %w", err)
	}
	if detail == nil || detail.Task == nil {
		return types.EvaluationStatuePending, nil
	}
	return detail.Task.Status, nil
}

func writeResult(path string, result *types.BenchmarkResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark result: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write benchmark result: %w", err)
	}
	return nil
}
