package evalgate

import (
	"os"
	"path/filepath"
	"testing"
)

func sampleConfig() GateConfig {
	return GateConfig{
		Baseline: map[string]float64{
			"precision": 0.108,
			"recall":    0.5,
			"ndcg3":     0.488,
			"ndcg10":    0.488,
			"mrr":       0.483,
			"map":       0.483,
		},
		Thresholds: map[string]float64{
			"precision": 0.02,
			"recall":    0.05,
			"ndcg3":     0.05,
			"ndcg10":    0.05,
			"mrr":       0.05,
			"map":       0.05,
		},
	}
}

func TestJudgeGate_Pass(t *testing.T) {
	current := map[string]float64{
		"precision": 0.120,
		"recall":    0.540,
		"ndcg3":     0.525,
		"ndcg10":    0.520,
		"mrr":       0.515,
		"map":       0.516,
	}
	report := JudgeGate(current, sampleConfig())
	if !report.Passed {
		t.Fatalf("expected pass, got regressions: %+v", report.Regressions)
	}
	if len(report.Regressions) != 0 {
		t.Fatalf("expected no regressions, got %d", len(report.Regressions))
	}
}

func TestJudgeGate_Regression(t *testing.T) {
	// recall 掉到 0.42（delta=-0.08），超过 0.05 容忍 → 判定退化。
	current := map[string]float64{
		"precision": 0.108,
		"recall":    0.42,
		"ndcg3":     0.488,
		"ndcg10":    0.488,
		"mrr":       0.483,
		"map":       0.483,
	}
	report := JudgeGate(current, sampleConfig())
	if report.Passed {
		t.Fatal("expected fail due to recall regression")
	}
	if len(report.Regressions) != 1 {
		t.Fatalf("expected exactly 1 regression, got %d: %+v", len(report.Regressions), report.Regressions)
	}
	r := report.Regressions[0]
	if r.Metric != "recall" {
		t.Fatalf("expected recall regression, got %q", r.Metric)
	}
	if r.Current != 0.42 || r.Baseline != 0.5 {
		t.Fatalf("wrong values: %+v", r)
	}
}

func TestJudgeGate_ImprovementNeverFails(t *testing.T) {
	// 所有指标提升（delta 为正）→ 通过。
	current := map[string]float64{
		"precision": 0.300,
		"recall":    0.900,
		"ndcg3":     0.900,
		"ndcg10":    0.900,
		"mrr":       0.900,
		"map":       0.900,
	}
	if report := JudgeGate(current, sampleConfig()); !report.Passed {
		t.Fatalf("improvements must never fail: %+v", report.Regressions)
	}
}

func TestJudgeGate_RejectsMetricsMissingBaseline(t *testing.T) {
	cfg := GateConfig{
		Baseline:   map[string]float64{"recall": 0.5},
		Thresholds: map[string]float64{"recall": 0.05, "unknown": 0.1},
	}
	// "unknown" 在 Thresholds 但不在 Baseline → 跳过，不 fail。
	if report := JudgeGate(map[string]float64{"recall": 0.6}, cfg); report.Passed || len(report.Errors) == 0 {
		t.Fatalf("missing baseline must fail: %+v", report)
	}
}

func TestJudgeGate_RejectsMetricsMissingFromCurrent(t *testing.T) {
	cfg := GateConfig{
		Baseline:   map[string]float64{"recall": 0.5, "mrr": 0.5},
		Thresholds: map[string]float64{"recall": 0.05, "mrr": 0.05},
	}
	// current 缺 mrr → 只判定 recall；recall 通过 → 整体通过。
	if report := JudgeGate(map[string]float64{"recall": 0.55}, cfg); report.Passed || len(report.Errors) == 0 {
		t.Fatalf("missing current metric must fail: %+v", report)
	}
}

func TestJudgeGate_DeterministicOrder(t *testing.T) {
	// 两个指标都退化 → 报告按指标名字典序排列，保证输出稳定可 diff。
	cfg := GateConfig{
		Baseline:   map[string]float64{"recall": 0.5, "precision": 0.3},
		Thresholds: map[string]float64{"recall": 0.05, "precision": 0.02},
	}
	report := JudgeGate(map[string]float64{"recall": 0.1, "precision": 0.1}, cfg)
	if len(report.Regressions) != 2 {
		t.Fatalf("expected 2 regressions, got %d", len(report.Regressions))
	}
	if report.Regressions[0].Metric != "precision" || report.Regressions[1].Metric != "recall" {
		t.Fatalf("expected [precision recall] order, got [%s %s]",
			report.Regressions[0].Metric, report.Regressions[1].Metric)
	}
}

func TestLoadGateConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate.json")
	content := `{"baseline":{"recall":0.5},"thresholds":{"recall":0.05}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadGateConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Baseline["recall"] != 0.5 || cfg.Thresholds["recall"] != 0.05 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadGateConfig_RejectsEmptyThresholds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(`{"baseline":{"recall":0.5},"thresholds":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGateConfig(path); err == nil {
		t.Fatal("expected error for empty thresholds")
	}
}

func TestFlattenEvaluationResponse(t *testing.T) {
	raw := []byte(`{
		"data": {
			"metric": {
				"retrieval_metrics": {"precision": 0.108, "recall": 0.5},
				"generation_metrics": {"bleu1": 0.5, "rougel": 0.6}
			}
		}
	}`)
	flat, err := FlattenEvaluationResponse(raw)
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if flat["recall"] != 0.5 || flat["bleu1"] != 0.5 || flat["rougel"] != 0.6 {
		t.Fatalf("unexpected flat map: %+v", flat)
	}
}

func TestFlattenEvaluationResponse_MissingMetric(t *testing.T) {
	// metric 缺失（如任务失败）→ 返回空 map 而非 panic，交给调用方处理。
	_, err := FlattenEvaluationResponse([]byte(`{"data":{}}`))
	if err == nil {
		t.Fatal("missing metrics must fail")
	}
}
