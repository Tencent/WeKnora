// Package reproduce runs the keyless deterministic evaluation regression workload.
package reproduce

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	retrievalfusion "github.com/Tencent/WeKnora/internal/retrieval/fusion"
	"github.com/Tencent/WeKnora/internal/types"
)

// ReportSchemaVersion identifies the machine-readable regression report contract.
const ReportSchemaVersion = 1

// Dataset is the checked-in deterministic evaluation fixture.
type Dataset struct {
	SchemaVersion int    `json:"schema_version"`
	DatasetID     string `json:"dataset_id"`
	KnowledgeID   string `json:"knowledge_id"`
	Passages      []struct {
		PID     string `json:"pid"`
		Content string `json:"content"`
	} `json:"passages"`
	Questions []struct {
		QID      string `json:"qid"`
		Question string `json:"question"`
		Answer   string `json:"answer"`
	} `json:"questions"`
	Relevance []struct {
		QID   string `json:"qid"`
		PID   string `json:"pid"`
		Grade int    `json:"grade"`
	} `json:"relevance"`
	Samples []struct {
		QID           string       `json:"qid"`
		VectorSearch  []RankedItem `json:"vector_search"`
		KeywordSearch []RankedItem `json:"keyword_search"`
		Search        []RankedItem `json:"search"`
		Rerank        []RankedItem `json:"rerank"`
		GeneratedText string       `json:"generated_text"`
	} `json:"samples"`
}

// RankedItem is one deterministic retriever or reranker output in the golden fixture.
type RankedItem struct {
	KnowledgeID string  `json:"knowledge_id"`
	ChunkIndex  int     `json:"chunk_index"`
	Score       float64 `json:"score"`
}

// Report is the authoritative regression artifact.
type Report struct {
	SchemaVersion int                                 `json:"schema_version"`
	Dataset       DatasetIdentity                     `json:"dataset"`
	Commit        string                              `json:"commit"`
	Configuration Configuration                       `json:"configuration"`
	MetricPlan    *types.EvaluationMetricPlanSnapshot `json:"metric_plan"`
	Metrics       []MetricResult                      `json:"metrics"`
	Environment   Environment                         `json:"environment"`
	Regression    RegressionResult                    `json:"regression"`
}

// DatasetIdentity pins the logical dataset and its canonical content hash.
type DatasetIdentity struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
	Fixture       string `json:"fixture"`
}

// Configuration records the resolved deterministic runner settings.
type Configuration struct {
	Pipeline     string `json:"pipeline"`
	Seed         int64  `json:"seed"`
	Concurrency  int    `json:"concurrency"`
	SampleCount  int    `json:"sample_count"`
	Network      string `json:"network"`
	ExternalKeys string `json:"external_keys"`
}

// Environment records the Go runtime facts relevant to reproduction.
type Environment struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	CPUs      int    `json:"cpus"`
}

// MetricResult records one resolved metric instance and its sample counts.
type MetricResult struct {
	Name         string   `json:"name"`
	InstanceID   string   `json:"instance_id"`
	Key          string   `json:"key"`
	Version      string   `json:"version"`
	ConfigSHA256 string   `json:"config_sha256"`
	Value        *float64 `json:"value"`
	Status       string   `json:"status"`
	NTotal       int      `json:"n_total"`
	NValid       int      `json:"n_valid"`
	NMissing     int      `json:"n_missing"`
}

// ThresholdConfig binds versioned regression limits to one dataset hash.
type ThresholdConfig struct {
	SchemaVersion int                  `json:"schema_version"`
	DatasetSHA256 string               `json:"dataset_content_sha256"`
	Metrics       map[string]Threshold `json:"metrics"`
}

// Threshold defines the baseline and permitted degradation for one metric.
type Threshold struct {
	Baseline               float64 `json:"baseline"`
	MaxAbsoluteDegradation float64 `json:"max_absolute_degradation"`
	Direction              string  `json:"direction"`
}

// RegressionResult contains the aggregate gate decision and per-metric checks.
type RegressionResult struct {
	Passed bool              `json:"passed"`
	Checks []RegressionCheck `json:"checks"`
}

// RegressionCheck is one diagnostic comparison against a frozen baseline.
type RegressionCheck struct {
	Metric        string  `json:"metric"`
	Baseline      float64 `json:"baseline"`
	Current       float64 `json:"current"`
	AbsoluteDelta float64 `json:"absolute_delta"`
	Threshold     float64 `json:"threshold"`
	Direction     string  `json:"direction"`
	Passed        bool    `json:"passed"`
}

// BuildOptions selects the fixtures and immutable report identity.
type BuildOptions struct {
	DatasetPath   string
	ThresholdPath string
	OverridePath  string
	Commit        string
	Environment   Environment
}

// RuntimeEnvironment returns the current Go runtime facts.
func RuntimeEnvironment() Environment {
	return Environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU()}
}

// Build executes production RRF fusion and the resolved metric plan on the golden dataset.
func Build(ctx context.Context, options BuildOptions) (*Report, error) {
	datasetRaw, err := os.ReadFile(options.DatasetPath)
	if err != nil {
		return nil, fmt.Errorf("read golden dataset: %w", err)
	}
	var dataset Dataset
	if err := json.Unmarshal(datasetRaw, &dataset); err != nil {
		return nil, fmt.Errorf("decode golden dataset: %w", err)
	}
	if dataset.SchemaVersion != 1 || dataset.DatasetID == "" || len(dataset.Samples) == 0 {
		return nil, errors.New("golden dataset identity and samples are required")
	}

	registry, err := metricregistry.NewDefaultRegistryWithPlugins(exactMatchMetric{})
	if err != nil {
		return nil, err
	}
	specs := append(metricregistry.DefaultSpecs(), metricregistry.Spec{
		Key: "generation.exact_match", Version: "1.0.0", Required: false,
	})
	plan, err := registry.Resolve(specs)
	if err != nil {
		return nil, err
	}
	perSample, err := computeSamples(ctx, &dataset, plan)
	if err != nil {
		return nil, err
	}
	aggregate := plan.Aggregate(perSample)
	metrics := reportMetrics(plan.Snapshot, aggregate, len(dataset.Samples))
	if err := applyOverrides(metrics, options.OverridePath); err != nil {
		return nil, err
	}

	thresholdRaw, err := os.ReadFile(options.ThresholdPath)
	if err != nil {
		return nil, fmt.Errorf("read regression thresholds: %w", err)
	}
	var thresholds ThresholdConfig
	if err := json.Unmarshal(thresholdRaw, &thresholds); err != nil {
		return nil, fmt.Errorf("decode regression thresholds: %w", err)
	}
	contentHash := types.CanonicalEvaluationDatasetContentSHA256(datasetContent(&dataset))
	if thresholds.SchemaVersion != 1 || thresholds.DatasetSHA256 != contentHash {
		return nil, errors.New("regression thresholds do not match the golden dataset content hash")
	}
	if err := validateThresholds(metrics, thresholds); err != nil {
		return nil, err
	}
	environment := options.Environment
	if environment.GoVersion == "" {
		environment = RuntimeEnvironment()
	}
	report := &Report{
		SchemaVersion: ReportSchemaVersion,
		Dataset: DatasetIdentity{
			ID: dataset.DatasetID, Version: dataset.SchemaVersion,
			ContentSHA256: contentHash, Fixture: "dataset/golden/v1/dataset.json",
		},
		Commit: options.Commit,
		Configuration: Configuration{
			Pipeline: "production_rrf_and_metric_plan", Seed: 42, Concurrency: 1,
			SampleCount: len(dataset.Samples), Network: "disabled", ExternalKeys: "unused",
		},
		MetricPlan: plan.Snapshot.Clone(), Metrics: metrics, Environment: environment,
	}
	report.Regression = CheckThresholds(metrics, thresholds)
	return report, nil
}

func validateThresholds(metrics []MetricResult, config ThresholdConfig) error {
	if len(config.Metrics) != len(metrics) {
		return fmt.Errorf(
			"regression thresholds contain %d metrics; resolved plan contains %d",
			len(config.Metrics), len(metrics),
		)
	}
	for _, metric := range metrics {
		threshold, exists := config.Metrics[metric.Name]
		if !exists {
			return fmt.Errorf("regression threshold for metric %s is required", metric.Name)
		}
		if threshold.Direction != "higher_is_better" {
			return fmt.Errorf(
				"regression threshold for metric %s has unsupported direction %q",
				metric.Name, threshold.Direction,
			)
		}
		if threshold.MaxAbsoluteDegradation < 0 {
			return fmt.Errorf("regression threshold for metric %s must be non-negative", metric.Name)
		}
	}
	return nil
}

func datasetContent(dataset *Dataset) *types.EvaluationDatasetVersionInput {
	input := &types.EvaluationDatasetVersionInput{}
	for _, passage := range dataset.Passages {
		input.Passages = append(input.Passages, types.EvaluationDatasetPassageInput{
			PID: passage.PID, Content: passage.Content,
		})
	}
	for _, question := range dataset.Questions {
		input.Questions = append(input.Questions, types.EvaluationDatasetQuestionInput{
			QID: question.QID, Question: question.Question, Answer: question.Answer,
		})
	}
	for _, edge := range dataset.Relevance {
		input.Relevance = append(input.Relevance, types.EvaluationDatasetRelevanceInput{
			QID: edge.QID, PID: edge.PID, Grade: edge.Grade,
		})
	}
	return input
}

func computeSamples(
	ctx context.Context,
	dataset *Dataset,
	plan *metricregistry.ResolvedPlan,
) ([]*types.MetricResult, error) {
	questions := make(map[string]struct{ answer string })
	for _, question := range dataset.Questions {
		questions[question.QID] = struct{ answer string }{answer: question.Answer}
	}
	relevance := make(map[string][]int)
	grades := make(map[string]map[int]int)
	labelsAvailable := make(map[string]bool)
	for _, edge := range dataset.Relevance {
		pid, err := strconv.Atoi(edge.PID)
		if err != nil {
			return nil, fmt.Errorf("golden pid %q is not numeric", edge.PID)
		}
		labelsAvailable[edge.QID] = true
		if grades[edge.QID] == nil {
			grades[edge.QID] = make(map[int]int)
		}
		grades[edge.QID][pid] = edge.Grade
		if edge.Grade > 0 {
			relevance[edge.QID] = append(relevance[edge.QID], pid)
		}
	}
	results := make([]*types.MetricResult, 0, len(dataset.Samples))
	for _, sample := range dataset.Samples {
		question, exists := questions[sample.QID]
		if !exists {
			return nil, fmt.Errorf("golden sample references unknown question %s", sample.QID)
		}
		ranked := sample.Search
		if len(sample.VectorSearch)+len(sample.KeywordSearch) > 0 {
			ranked = rankedItemsFromIndexes(retrievalfusion.Results(
				ctx,
				indexesFromRankedItems(sample.VectorSearch),
				indexesFromRankedItems(sample.KeywordSearch),
				nil,
			))
		}
		if len(sample.Rerank) > 0 {
			ranked = sample.Rerank
		}
		retrieved := make([]int, 0, len(ranked))
		seen := make(map[int]struct{})
		for _, item := range ranked {
			pid := -1
			if item.KnowledgeID == dataset.KnowledgeID && item.ChunkIndex >= 0 {
				if _, duplicate := seen[item.ChunkIndex]; !duplicate {
					pid = item.ChunkIndex
					seen[pid] = struct{}{}
				}
			}
			retrieved = append(retrieved, pid)
		}
		computed, _, err := plan.Compute(ctx, &types.MetricInput{
			RetrievalGT: [][]int{relevance[sample.QID]}, RetrievalGrades: grades[sample.QID],
			RetrievalLabelsAvailable: labelsAvailable[sample.QID], RetrievalIDs: retrieved,
			GeneratedTexts: sample.GeneratedText, GeneratedGT: question.answer,
		})
		if err != nil {
			return nil, fmt.Errorf("compute golden sample %s: %w", sample.QID, err)
		}
		results = append(results, computed)
	}
	return results, nil
}

func indexesFromRankedItems(items []RankedItem) []*types.IndexWithScore {
	results := make([]*types.IndexWithScore, 0, len(items))
	for _, item := range items {
		results = append(results, &types.IndexWithScore{
			ChunkID:     fmt.Sprintf("%s/%d", item.KnowledgeID, item.ChunkIndex),
			KnowledgeID: item.KnowledgeID,
			ID:          strconv.Itoa(item.ChunkIndex),
			Score:       item.Score,
		})
	}
	return results
}

func rankedItemsFromIndexes(items []*types.IndexWithScore) []RankedItem {
	results := make([]RankedItem, 0, len(items))
	for _, item := range items {
		chunkIndex, _ := strconv.Atoi(item.ID)
		results = append(results, RankedItem{
			KnowledgeID: item.KnowledgeID, ChunkIndex: chunkIndex, Score: item.Score,
		})
	}
	return results
}

func reportMetrics(plan *types.EvaluationMetricPlanSnapshot, aggregate *types.MetricResult, total int) []MetricResult {
	metrics := make([]MetricResult, 0, len(plan.Metrics))
	for _, spec := range plan.Metrics {
		score := aggregate.Scores[spec.InstanceID]
		valid := 0
		if score.Value != nil && score.Status == types.EvaluationMetricObservationValid {
			valid = total
		}
		metrics = append(metrics, MetricResult{
			Name: metricName(spec), InstanceID: spec.InstanceID, Key: spec.Key, Version: spec.Version,
			ConfigSHA256: spec.ConfigSHA256, Value: score.Value, Status: score.Status,
			NTotal: total, NValid: valid, NMissing: total - valid,
		})
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	return metrics
}

func metricName(spec types.EvaluationMetricSpecSnapshot) string {
	var config map[string]any
	_ = json.Unmarshal(spec.Config, &config)
	if value, exists := config["k"]; exists {
		return fmt.Sprintf("%s.k%v", spec.Key, value)
	}
	if value, exists := config["n"]; exists {
		return fmt.Sprintf("%s.n%v", spec.Key, value)
	}
	if value, exists := config["variant"]; exists {
		return fmt.Sprintf("%s.%v", spec.Key, value)
	}
	return spec.Key
}

func applyOverrides(metrics []MetricResult, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read metric overrides: %w", err)
	}
	var payload struct {
		Metrics map[string]float64 `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode metric overrides: %w", err)
	}
	for name, value := range payload.Metrics {
		found := false
		for index := range metrics {
			if metrics[index].Name == name {
				metrics[index].Value = &value
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("metric override %s is unknown", name)
		}
	}
	return nil
}

// CheckThresholds compares metric values with the versioned baseline limits.
func CheckThresholds(metrics []MetricResult, config ThresholdConfig) RegressionResult {
	byName := make(map[string]MetricResult, len(metrics))
	for _, metric := range metrics {
		byName[metric.Name] = metric
	}
	names := make([]string, 0, len(config.Metrics))
	for name := range config.Metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	result := RegressionResult{Passed: true, Checks: make([]RegressionCheck, 0, len(names))}
	for _, name := range names {
		threshold := config.Metrics[name]
		metric, exists := byName[name]
		check := RegressionCheck{
			Metric: name, Baseline: threshold.Baseline,
			Threshold: threshold.MaxAbsoluteDegradation, Direction: threshold.Direction,
		}
		if exists && metric.Value != nil && threshold.Direction == "higher_is_better" {
			check.Current = *metric.Value
			check.AbsoluteDelta = threshold.Baseline - check.Current
			check.Passed = check.AbsoluteDelta <= threshold.MaxAbsoluteDegradation+1e-12
		}
		if !check.Passed {
			result.Passed = false
		}
		result.Checks = append(result.Checks, check)
	}
	return result
}

// JSON encodes the authoritative report with stable indentation.
func JSON(report *Report) ([]byte, error) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// Markdown renders a human-readable view from an in-memory authoritative report.
func Markdown(report *Report) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "# Evaluation regression report\n\n")
	fmt.Fprintf(
		&output,
		"Dataset `%s` version %d has content SHA-256 `%s`. Production RRF fusion and the "+
			"resolved metric plan ran at commit `%s` without network access or external keys.\n\n",
		report.Dataset.ID, report.Dataset.Version, report.Dataset.ContentSHA256, report.Commit,
	)
	fmt.Fprintf(
		&output,
		"The resolved plan contains %d metric instances. The table shows the current value, "+
			"valid sample count, frozen baseline, permitted absolute degradation, and gate result.\n\n",
		len(report.MetricPlan.Metrics),
	)
	fmt.Fprintf(
		&output,
		"| Metric | Current | Samples | Baseline | Threshold | Gate |\n"+
			"| --- | ---: | ---: | ---: | ---: | --- |\n",
	)
	current := make(map[string]MetricResult, len(report.Metrics))
	for _, metric := range report.Metrics {
		current[metric.Name] = metric
	}
	for _, check := range report.Regression.Checks {
		metric := current[check.Metric]
		value := "null"
		if metric.Value != nil {
			value = strconv.FormatFloat(*metric.Value, 'g', 12, 64)
		}
		gate := "PASS"
		if !check.Passed {
			gate = "FAIL"
		}
		fmt.Fprintf(
			&output, "| `%s` | %s | %d/%d | %.12g | %.12g | %s |\n",
			check.Metric, value, metric.NValid, metric.NTotal, check.Baseline, check.Threshold, gate,
		)
	}
	fmt.Fprintf(
		&output, "\nThe gate result is **%s**. Runtime: %s on %s/%s with %d logical CPUs.\n",
		map[bool]string{true: "PASS", false: "FAIL"}[report.Regression.Passed],
		report.Environment.GoVersion, report.Environment.GOOS,
		report.Environment.GOARCH, report.Environment.CPUs,
	)
	return output.Bytes()
}

type exactMatchMetric struct{}

func (exactMatchMetric) Definition() metricregistry.Definition {
	return metricregistry.Definition{
		Key: "generation.exact_match", Version: "1.0.0", Kind: metricregistry.KindGeneration,
		Description:   "Exact equality of generated and reference text.",
		DefaultConfig: json.RawMessage(`{}`),
		ConfigSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}
}

func (exactMatchMetric) Validate(config json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return err
	}
	if len(object) != 0 {
		return errors.New("exact match configuration must be empty")
	}
	return nil
}

func (exactMatchMetric) Compute(
	_ context.Context,
	input *types.MetricInput,
	_ json.RawMessage,
) (metricregistry.Observation, error) {
	value := 0.0
	if input != nil && input.GeneratedTexts == input.GeneratedGT {
		value = 1
	}
	return metricregistry.Observation{Value: &value, Status: types.EvaluationMetricObservationValid}, nil
}
