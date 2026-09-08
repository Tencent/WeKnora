package regression

import (
	"fmt"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Status is the binary outcome of a single metric comparison or the whole gate.
type Status string

const (
	// StatusPass indicates that no monitored metric regressed.
	StatusPass Status = "PASS"
	// StatusFail indicates that at least one monitored metric failed.
	StatusFail Status = "FAIL"
)

// MetricComparison is the per-metric result of comparing current against
// baseline. Baseline/Current/Delta pointers are nil only when the metric is
// missing or non-finite, which is reported explicitly and treated as a failure.
type MetricComparison struct {
	Key            MetricKey `json:"key"`
	DisplayName    string    `json:"display_name"`
	HigherIsBetter bool      `json:"higher_is_better"`
	Missing        bool      `json:"missing"`
	NonFinite      bool      `json:"non_finite"`
	Baseline       *float64  `json:"baseline,omitempty"`
	Current        *float64  `json:"current,omitempty"`
	Delta          *float64  `json:"delta,omitempty"`
	AllowedDrop    float64   `json:"allowed_drop"`
	Status         Status    `json:"status"`
}

// Report is the machine-readable regression result. It is small and auditable:
// one overall status, the identifiers of both sides, one comparison per metric,
// and the list of failures.
type Report struct {
	OverallStatus  Status             `json:"overall_status"`
	BaselineID     string             `json:"baseline_id"`
	CurrentID      string             `json:"current_id"`
	Comparisons    []MetricComparison `json:"comparisons"`
	FailedMetrics  []string           `json:"failed_metrics"`
	MissingMetrics []string           `json:"missing_metrics"`
	Summary        string             `json:"summary"`
}

// Compare compares a current unified Benchmark result against a frozen baseline
// under the given policy. It fails closed: a missing or non-finite metric is a
// failure, never a silent pass.
func Compare(baseline, current *types.BenchmarkResult, policy Policy) (*Report, error) {
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid regression policy: %w", err)
	}
	if baseline == nil {
		return nil, fmt.Errorf("baseline result is nil")
	}
	if current == nil {
		return nil, fmt.Errorf("current result is nil")
	}

	baseVals := extract(baseline)
	currVals := extract(current)

	comparisons := make([]MetricComparison, 0, len(baseVals))
	var failed, missing []string
	for i := range baseVals {
		mc := compareOne(baseVals[i], currVals[i], policy)
		comparisons = append(comparisons, mc)
		switch {
		case mc.Missing:
			missing = append(missing, string(mc.Key))
		case mc.Status == StatusFail:
			failed = append(failed, string(mc.Key))
		}
	}

	overall := StatusPass
	if len(failed) > 0 || len(missing) > 0 {
		overall = StatusFail
	}

	return &Report{
		OverallStatus:  overall,
		BaselineID:     resultID(baseline),
		CurrentID:      resultID(current),
		Comparisons:    comparisons,
		FailedMetrics:  failed,
		MissingMetrics: missing,
		Summary:        summarize(overall, failed, missing),
	}, nil
}

func compareOne(base, curr metricValue, policy Policy) MetricComparison {
	mc := MetricComparison{
		Key:            base.spec.Key,
		DisplayName:    base.spec.DisplayName,
		HigherIsBetter: base.spec.HigherIsBetter,
		AllowedDrop:    policy.AllowedDropFor(base.spec.Key),
	}
	if base.value == nil || curr.value == nil {
		mc.Missing = true
		mc.Status = StatusFail
		return mc
	}
	b, c := *base.value, *curr.value
	if !isFinite(b) || !isFinite(c) {
		mc.NonFinite = true
		mc.Status = StatusFail
		return mc
	}
	delta := c - b
	mc.Baseline = &b
	mc.Current = &c
	mc.Delta = &delta
	mc.Status = StatusPass
	if mc.HigherIsBetter && delta < -mc.AllowedDrop {
		mc.Status = StatusFail
	}
	if !mc.HigherIsBetter && delta > mc.AllowedDrop {
		mc.Status = StatusFail
	}
	return mc
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func resultID(r *types.BenchmarkResult) string {
	if r.Run.TaskID != "" {
		return r.Run.TaskID
	}
	if r.Run.EvaluationRunID != "" {
		return r.Run.EvaluationRunID
	}
	if r.BenchmarkVersion != "" {
		return r.BenchmarkVersion
	}
	return "unknown"
}

func summarize(overall Status, failed, missing []string) string {
	if overall == StatusPass {
		return "All monitored quality metrics are within regression thresholds."
	}
	var parts []string
	if len(failed) > 0 {
		parts = append(parts, fmt.Sprintf("%s exceeded allowed regression threshold", metricCount(len(failed))))
	}
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("%s missing", metricCount(len(missing))))
	}
	return strings.Join(parts, "; ")
}

func metricCount(n int) string {
	if n == 1 {
		return "1 quality metric"
	}
	return fmt.Sprintf("%d quality metrics", n)
}
