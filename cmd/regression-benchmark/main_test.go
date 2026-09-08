package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type evalServiceStub struct {
	start  func(ctx context.Context, datasetID, kbID, chatID, rerankID string) (*types.EvaluationDetail, error)
	result func(ctx context.Context, taskID string) (*types.EvaluationDetail, error)
}

func (s *evalServiceStub) Evaluation(
	ctx context.Context, datasetID, kbID, chatID, rerankID string,
) (*types.EvaluationDetail, error) {
	return s.start(ctx, datasetID, kbID, chatID, rerankID)
}

func (s *evalServiceStub) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	return s.result(ctx, taskID)
}

type resultServiceStub struct {
	get func(ctx context.Context, taskID string) (*types.BenchmarkResult, error)
}

func (s *resultServiceStub) GetBenchmarkResult(ctx context.Context, taskID string) (*types.BenchmarkResult, error) {
	return s.get(ctx, taskID)
}

func detail(id string, status types.EvaluationStatue) *types.EvaluationDetail {
	return &types.EvaluationDetail{Task: &types.EvaluationTask{ID: id, Status: status}}
}

func TestBenchmarkContextCarriesTenant(t *testing.T) {
	ctx := benchmarkContext(10000)

	id, ok := types.TenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, uint64(10000), id)

	info, ok := types.TenantInfoFromContext(ctx)
	require.True(t, ok)
	require.NotNil(t, info)
	require.Equal(t, uint64(10000), info.ID)
}

func TestRunBenchmarkSuccess(t *testing.T) {
	var startedDataset, fetchedTask string
	polls := 0
	eval := &evalServiceStub{
		start: func(_ context.Context, datasetID, _, _, _ string) (*types.EvaluationDetail, error) {
			startedDataset = datasetID
			return detail("task-1", types.EvaluationStatuePending), nil
		},
		result: func(_ context.Context, taskID string) (*types.EvaluationDetail, error) {
			polls++
			if polls < 2 {
				return detail(taskID, types.EvaluationStatuePending), nil
			}
			return detail(taskID, types.EvaluationStatueSuccess), nil
		},
	}
	want := &types.BenchmarkResult{
		BenchmarkVersion: "v1.1",
		Run:              types.BenchmarkRunSummary{TaskID: "task-1"},
	}
	results := &resultServiceStub{
		get: func(_ context.Context, taskID string) (*types.BenchmarkResult, error) {
			fetchedTask = taskID
			return want, nil
		},
	}

	got, err := runBenchmark(context.Background(), eval, results, "benchmark_v1", time.Minute, time.Millisecond)
	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, "benchmark_v1", startedDataset)
	require.Equal(t, "task-1", fetchedTask)
}

func TestRunBenchmarkFailed(t *testing.T) {
	eval := &evalServiceStub{
		start: func(_ context.Context, _, _, _, _ string) (*types.EvaluationDetail, error) {
			return detail("task-1", types.EvaluationStatuePending), nil
		},
		result: func(_ context.Context, taskID string) (*types.EvaluationDetail, error) {
			return detail(taskID, types.EvaluationStatueFailed), nil
		},
	}
	results := &resultServiceStub{get: func(_ context.Context, _ string) (*types.BenchmarkResult, error) {
		t.Fatal("GetBenchmarkResult must not be called on a failed run")
		return nil, nil
	}}

	_, err := runBenchmark(context.Background(), eval, results, "benchmark_v1", time.Minute, time.Millisecond)
	require.ErrorContains(t, err, "failed")
}

func TestRunBenchmarkTimeout(t *testing.T) {
	eval := &evalServiceStub{
		start: func(_ context.Context, _, _, _, _ string) (*types.EvaluationDetail, error) {
			return detail("task-1", types.EvaluationStatuePending), nil
		},
		result: func(_ context.Context, taskID string) (*types.EvaluationDetail, error) {
			return detail(taskID, types.EvaluationStatueRunning), nil
		},
	}
	results := &resultServiceStub{get: func(_ context.Context, _ string) (*types.BenchmarkResult, error) {
		t.Fatal("GetBenchmarkResult must not be called on timeout")
		return nil, nil
	}}

	_, err := runBenchmark(context.Background(), eval, results, "benchmark_v1", 30*time.Millisecond, 5*time.Millisecond)
	require.ErrorContains(t, err, "timed out")
}

func TestRunBenchmarkNilResult(t *testing.T) {
	eval := &evalServiceStub{
		start: func(_ context.Context, _, _, _, _ string) (*types.EvaluationDetail, error) {
			return detail("task-1", types.EvaluationStatuePending), nil
		},
		result: func(_ context.Context, taskID string) (*types.EvaluationDetail, error) {
			return detail(taskID, types.EvaluationStatueSuccess), nil
		},
	}
	// Abnormal service: a successful run yields a nil result with no error.
	results := &resultServiceStub{get: func(_ context.Context, _ string) (*types.BenchmarkResult, error) {
		return nil, nil
	}}

	_, err := runBenchmark(context.Background(), eval, results, "benchmark_v1", time.Minute, time.Millisecond)
	require.ErrorContains(t, err, "empty result")
}

func TestRunBenchmarkStartError(t *testing.T) {
	eval := &evalServiceStub{
		start: func(_ context.Context, _, _, _, _ string) (*types.EvaluationDetail, error) {
			return nil, os.ErrNotExist
		},
	}
	_, err := runBenchmark(
		context.Background(), eval, &resultServiceStub{}, "benchmark_v1", time.Minute, time.Millisecond,
	)
	require.ErrorContains(t, err, "start benchmark evaluation")
}

func TestWriteResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "current.json")

	v := 0.5
	result := &types.BenchmarkResult{
		BenchmarkVersion: "v1.1",
		Run:              types.BenchmarkRunSummary{TaskID: "task-1"},
		Quality: types.BenchmarkQuality{
			State:     types.BenchmarkQualityStateComplete,
			Retrieval: &types.BenchmarkRetrievalQuality{Recall: &v},
		},
	}
	require.NoError(t, writeResult(path, result))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(data), "\n"), "result JSON must end with a newline")

	var decoded types.BenchmarkResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "task-1", decoded.Run.TaskID)
	require.Equal(t, types.BenchmarkQualityStateComplete, decoded.Quality.State)
	require.NotNil(t, decoded.Quality.Retrieval.Recall)
	require.InDelta(t, 0.5, *decoded.Quality.Retrieval.Recall, 1e-9)
}
