package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvaluatePassesThresholds(t *testing.T) {
	result := decodeResult(t, `{
		"task":{"status":2,"total":10,"finished":10,"duration_ms":1200},
		"metric":{"retrieval_metrics":{"recall":0.8}},
		"usage":{"cache_hit_rate":0.4,"cost_by_currency":{"USD":0.05}}
	}`)
	report := evaluate(result, nil, baseline{
		RequireSuccess: true, RequireComplete: true,
		Minimum: map[string]float64{"metric.retrieval_metrics.recall": 0.75},
		Maximum: map[string]float64{"task.duration_ms": 2000, "usage.cost_by_currency.USD": 0.1},
	})
	require.True(t, report.Passed)
	require.Len(t, report.Checks, 5)
}

func TestRunLocalEvaluationStartsAndPollsToCompletion(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"success":true,"data":{"task":{"id":"task-1","status":0}}}`))
			return
		}
		require.Equal(t, "task-1", request.URL.Query().Get("task_id"))
		if polls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"success":true,"data":{"task":{"id":"task-1","status":1}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"task":{"id":"task-1","status":2,"total":1,"finished":1}}}`))
	}))
	defer server.Close()

	result, err := runLocalEvaluation(context.Background(), localEvaluationOptions{
		BaseURL: server.URL, APIKey: "test-key", DatasetID: "default",
		PollInterval: time.Millisecond, Client: server.Client(),
	})
	require.NoError(t, err)
	status, ok := lookup(unwrapAPIData(result), "task.status")
	require.True(t, ok)
	require.Equal(t, float64(2), status)
	require.Equal(t, int32(2), polls.Load())
}

func TestRunLocalEvaluationRejectsNonLoopbackURL(t *testing.T) {
	_, err := runLocalEvaluation(context.Background(), localEvaluationOptions{
		BaseURL: "https://example.com", PollInterval: time.Second,
	})
	require.ErrorContains(t, err, "localhost or a loopback IP")
}

func TestEvaluateReportsMissingAndRegression(t *testing.T) {
	result := decodeResult(t, `{
		"task":{"status":3,"total":10,"finished":8},
		"metric":{"retrieval_metrics":{"recall":0.4}}
	}`)
	report := evaluate(result, nil, baseline{
		RequireSuccess: true, RequireComplete: true,
		Minimum: map[string]float64{"metric.retrieval_metrics.recall": 0.75},
		Maximum: map[string]float64{"usage.cost_by_currency.USD": 0.1},
	})
	require.False(t, report.Passed)
	require.False(t, report.Checks[0].Passed)
	require.False(t, report.Checks[len(report.Checks)-1].Passed)
	require.NotEmpty(t, report.Checks[len(report.Checks)-1].Error)
}

func TestEvaluateIdentifiesRecallRegression(t *testing.T) {
	result := decodeResult(t, `{
		"task":{"status":2,"total":10,"finished":10},
		"metric":{"retrieval_metrics":{"recall":0.4}}
	}`)
	report := evaluate(result, nil, baseline{
		RequireSuccess:  true,
		RequireComplete: true,
		Minimum: map[string]float64{
			"metric.retrieval_metrics.recall": 0.5,
		},
	})

	require.False(t, report.Passed)
	require.Len(t, report.Checks, 3)
	recallCheck := report.Checks[2]
	require.Equal(t, "metric.retrieval_metrics.recall", recallCheck.Path)
	require.Equal(t, "minimum", recallCheck.Rule)
	require.Equal(t, 0.5, recallCheck.Expected)
	require.Equal(t, 0.4, recallCheck.Actual)
	require.False(t, recallCheck.Passed)
}

func TestEvaluateComparesCandidateWithReference(t *testing.T) {
	reference := decodeResult(t, `{
		"metric":{"retrieval_metrics":{"recall":0.80}},
		"task":{"duration_ms":1000}
	}`)
	candidate := decodeResult(t, `{
		"metric":{"retrieval_metrics":{"recall":0.76}},
		"task":{"duration_ms":1250}
	}`)
	report := evaluate(candidate, reference, baseline{
		MaximumDecrease: map[string]float64{"metric.retrieval_metrics.recall": 0.05},
		MaximumIncrease: map[string]float64{"task.duration_ms": 200},
	})

	require.False(t, report.Passed)
	require.Len(t, report.Checks, 2)
	recallCheck := report.Checks[0]
	require.True(t, recallCheck.Passed)
	require.NotNil(t, recallCheck.Reference)
	require.InDelta(t, 0.80, *recallCheck.Reference, 1e-9)
	require.NotNil(t, recallCheck.Delta)
	require.InDelta(t, -0.04, *recallCheck.Delta, 1e-9)
	durationCheck := report.Checks[1]
	require.False(t, durationCheck.Passed)
	require.InDelta(t, 250, *durationCheck.Delta, 1e-9)
}

func TestEvaluateReportsMissingReferenceValue(t *testing.T) {
	candidate := decodeResult(t, `{"metric":{"retrieval_metrics":{"recall":0.76}}}`)
	report := evaluate(candidate, map[string]any{}, baseline{
		MaximumDecrease: map[string]float64{"metric.retrieval_metrics.recall": 0.05},
	})

	require.False(t, report.Passed)
	require.Equal(t, "reference value is missing or not numeric", report.Checks[0].Error)
	require.Nil(t, report.Checks[0].Reference)
	require.Nil(t, report.Checks[0].Delta)
}

func TestValidateBaselineRejectsNegativeRelativeTolerance(t *testing.T) {
	err := validateBaseline(baseline{MaximumDecrease: map[string]float64{"metric.recall": -0.01}})
	require.ErrorContains(t, err, "must not be negative")
}

func TestUnwrapAPIData(t *testing.T) {
	result := decodeResult(t, `{"success":true,"data":{"task":{"status":"success"}}}`)
	unwrapped := unwrapAPIData(result)
	status, ok := lookup(unwrapped, "task.status")
	require.True(t, ok)
	require.Equal(t, "success", status)
}

func decodeResult(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	return result
}
