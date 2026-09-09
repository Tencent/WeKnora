package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type runInput struct {
	Experiment experimentProtocol `json:"experiment"`
	Embedding  embeddingRun       `json:"embedding"`
	ModelCalls []modelCall        `json:"model_calls"`
	Calls      []modelCall        `json:"calls"`
}

// experimentProtocol proves that two measurements came from the same frozen
// workload without retaining prompt or document bodies.
type experimentProtocol struct {
	ProtocolVersion          int    `json:"protocol_version"`
	Cohort                   string `json:"cohort"`
	WorkloadFingerprint      string `json:"workload_fingerprint"`
	ModelFingerprint         string `json:"model_fingerprint"`
	ConfigurationFingerprint string `json:"configuration_fingerprint"`
	Repetitions              int    `json:"repetitions"`
}

type embeddingRun struct {
	RequestedTexts int `json:"requested_texts"`
	ProviderCalls  int `json:"provider_calls"`
}

type modelCall struct {
	ModelID                 string  `json:"model_id"`
	ModelType               string  `json:"model_type"`
	Purpose                 string  `json:"purpose"`
	PromptPrefixFingerprint string  `json:"prompt_prefix_fingerprint"`
	Usage                   usage   `json:"usage"`
	Pricing                 pricing `json:"pricing"`
	EstimatedCost           float64 `json:"estimated_cost"`
	DurationMS              int64   `json:"duration_ms"`
	Success                 bool    `json:"success"`
}

type usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CacheReadTokens  int  `json:"cache_read_tokens"`
	CacheWriteTokens int  `json:"cache_write_tokens"`
	CacheMissTokens  int  `json:"cache_miss_tokens"`
	CacheReported    bool `json:"cache_reported"`
}

type pricing struct {
	Currency string `json:"currency"`
}

type wikiSummary struct {
	CallCount          int                `json:"call_count"`
	CacheReportedCalls int                `json:"cache_reported_calls"`
	CacheHitCalls      int                `json:"cache_hit_calls"`
	PromptTokens       int                `json:"prompt_tokens"`
	CacheReadTokens    int                `json:"cache_read_tokens"`
	CacheWriteTokens   int                `json:"cache_write_tokens"`
	CacheMissTokens    int                `json:"cache_miss_tokens"`
	CacheHitRate       float64            `json:"cache_hit_rate"`
	MedianLatencyMS    float64            `json:"median_latency_ms"`
	P95LatencyMS       float64            `json:"p95_latency_ms"`
	CostByCurrency     map[string]float64 `json:"cost_by_currency"`
	CostPerKPrompt     map[string]float64 `json:"cost_per_1k_prompt_tokens"`
}

type comparisonReport struct {
	PurposePrefix string `json:"purpose_prefix"`
	Strict        bool   `json:"strict_protocol_validated"`
	Embedding     struct {
		BeforeRequestedTexts int     `json:"before_requested_texts"`
		AfterRequestedTexts  int     `json:"after_requested_texts"`
		BeforeProviderCalls  int     `json:"before_provider_calls"`
		AfterProviderCalls   int     `json:"after_provider_calls"`
		CallsSaved           int     `json:"calls_saved"`
		ReductionRate        float64 `json:"reduction_rate"`
	} `json:"embedding"`
	Wiki struct {
		Before            wikiSummary `json:"before"`
		After             wikiSummary `json:"after"`
		CacheHitRateDelta float64     `json:"cache_hit_rate_delta"`
	} `json:"wiki"`
}

func main() {
	beforePath := flag.String("before", "", "before-run JSON file")
	afterPath := flag.String("after", "", "after-run JSON file")
	purposePrefix := flag.String("purpose-prefix", "wiki_", "model-call purpose prefix")
	strict := flag.Bool("strict", false, "require a matched, repeatable cold/warm protocol")
	reportPath := flag.String("report", "", "optional comparison report output")
	flag.Parse()
	if *beforePath == "" || *afterPath == "" {
		fatal(errors.New("-before and -after are required"))
	}

	before, err := readRun(*beforePath)
	if err != nil {
		fatal(fmt.Errorf("read before run: %w", err))
	}
	after, err := readRun(*afterPath)
	if err != nil {
		fatal(fmt.Errorf("read after run: %w", err))
	}
	if *strict {
		if err := validateStrictPair(before, after, *purposePrefix); err != nil {
			fatal(fmt.Errorf("strict protocol validation: %w", err))
		}
	}
	report := compareRuns(before, after, *purposePrefix)
	report.Strict = *strict
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(encoded))
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
}

func compareRuns(before, after runInput, purposePrefix string) comparisonReport {
	report := comparisonReport{PurposePrefix: purposePrefix}
	report.Embedding.BeforeRequestedTexts = before.Embedding.RequestedTexts
	report.Embedding.AfterRequestedTexts = after.Embedding.RequestedTexts
	report.Embedding.BeforeProviderCalls = before.Embedding.ProviderCalls
	report.Embedding.AfterProviderCalls = after.Embedding.ProviderCalls
	report.Embedding.CallsSaved = before.Embedding.ProviderCalls - after.Embedding.ProviderCalls
	if before.Embedding.ProviderCalls > 0 {
		report.Embedding.ReductionRate = float64(report.Embedding.CallsSaved) /
			float64(before.Embedding.ProviderCalls)
	}
	report.Wiki.Before = summarizeCalls(allCalls(before), purposePrefix)
	report.Wiki.After = summarizeCalls(allCalls(after), purposePrefix)
	report.Wiki.CacheHitRateDelta = report.Wiki.After.CacheHitRate - report.Wiki.Before.CacheHitRate
	return report
}

func summarizeCalls(calls []modelCall, purposePrefix string) wikiSummary {
	summary := wikiSummary{
		CostByCurrency: make(map[string]float64), CostPerKPrompt: make(map[string]float64),
	}
	latencies := make([]int64, 0, len(calls))
	for _, call := range calls {
		if !strings.HasPrefix(call.Purpose, purposePrefix) {
			continue
		}
		summary.CallCount++
		summary.PromptTokens += call.Usage.PromptTokens
		summary.CacheReadTokens += call.Usage.CacheReadTokens
		summary.CacheWriteTokens += call.Usage.CacheWriteTokens
		summary.CacheMissTokens += call.Usage.CacheMissTokens
		latencies = append(latencies, call.DurationMS)
		if call.Usage.CacheReported {
			summary.CacheReportedCalls++
			if call.Usage.CacheReadTokens > 0 {
				summary.CacheHitCalls++
			}
		}
		currency := strings.ToUpper(strings.TrimSpace(call.Pricing.Currency))
		if currency != "" {
			summary.CostByCurrency[currency] += call.EstimatedCost
		}
	}
	reportedPromptTokens := summary.CacheReadTokens + summary.CacheMissTokens
	if reportedPromptTokens > 0 {
		summary.CacheHitRate = float64(summary.CacheReadTokens) / float64(reportedPromptTokens)
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		summary.MedianLatencyMS = percentile(latencies, 0.5)
		summary.P95LatencyMS = percentile(latencies, 0.95)
	}
	if summary.PromptTokens > 0 {
		for currency, cost := range summary.CostByCurrency {
			summary.CostPerKPrompt[currency] = cost * 1000 / float64(summary.PromptTokens)
		}
	}
	return summary
}

func percentile(sortedValues []int64, quantile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if quantile == 0.5 && len(sortedValues)%2 == 0 {
		middle := len(sortedValues) / 2
		return float64(sortedValues[middle-1]+sortedValues[middle]) / 2
	}
	index := int(math.Ceil(quantile*float64(len(sortedValues)))) - 1
	index = max(0, min(index, len(sortedValues)-1))
	return float64(sortedValues[index])
}

func validateStrictPair(before, after runInput, purposePrefix string) error {
	if before.Experiment.ProtocolVersion != 1 || after.Experiment.ProtocolVersion != 1 {
		return errors.New("both inputs must use experiment protocol_version 1")
	}
	if !strings.EqualFold(before.Experiment.Cohort, "cold") ||
		!strings.EqualFold(after.Experiment.Cohort, "warm") {
		return errors.New("before cohort must be cold and after cohort must be warm")
	}
	if before.Experiment.Repetitions < 3 || before.Experiment.Repetitions != after.Experiment.Repetitions {
		return errors.New("both cohorts must use the same repetitions value of at least 3")
	}
	for _, field := range []struct {
		name, before, after string
	}{
		{"workload_fingerprint", before.Experiment.WorkloadFingerprint, after.Experiment.WorkloadFingerprint},
		{"model_fingerprint", before.Experiment.ModelFingerprint, after.Experiment.ModelFingerprint},
		{"configuration_fingerprint", before.Experiment.ConfigurationFingerprint, after.Experiment.ConfigurationFingerprint},
	} {
		if strings.TrimSpace(field.before) == "" || field.before != field.after {
			return fmt.Errorf("%s must be non-empty and identical", field.name)
		}
	}

	beforeSignatures, err := strictCallSignatures(allCalls(before), purposePrefix)
	if err != nil {
		return fmt.Errorf("cold cohort: %w", err)
	}
	afterSignatures, err := strictCallSignatures(allCalls(after), purposePrefix)
	if err != nil {
		return fmt.Errorf("warm cohort: %w", err)
	}
	if !equalSignatureCounts(beforeSignatures, afterSignatures) {
		return errors.New("Wiki call count or prompt-prefix signatures differ between cohorts")
	}
	if len(beforeSignatures) != before.Experiment.Repetitions {
		return errors.New("repetitions must equal the number of distinct matched Wiki call signatures")
	}
	for _, count := range beforeSignatures {
		if count != 1 {
			return errors.New("each Wiki call signature must occur exactly once per cohort")
		}
	}
	beforeSummary := summarizeCalls(allCalls(before), purposePrefix)
	afterSummary := summarizeCalls(allCalls(after), purposePrefix)
	if beforeSummary.CacheReportedCalls != beforeSummary.CallCount ||
		afterSummary.CacheReportedCalls != afterSummary.CallCount {
		return errors.New("every Wiki call must contain provider-reported cache telemetry")
	}
	if beforeSummary.CacheHitCalls != 0 {
		return errors.New("cold cohort contains cache hits")
	}
	if afterSummary.CacheHitCalls == 0 {
		return errors.New("warm cohort contains no cache hits")
	}
	return nil
}

func strictCallSignatures(calls []modelCall, purposePrefix string) (map[string]int, error) {
	signatures := make(map[string]int)
	for _, call := range calls {
		if !strings.HasPrefix(call.Purpose, purposePrefix) {
			continue
		}
		if !call.Success {
			return nil, fmt.Errorf("call for purpose %q was not successful", call.Purpose)
		}
		if call.ModelID == "" || call.PromptPrefixFingerprint == "" {
			return nil, errors.New("every Wiki call must include model_id and prompt_prefix_fingerprint")
		}
		signatures[call.ModelID+"\x00"+call.Purpose+"\x00"+call.PromptPrefixFingerprint]++
	}
	if len(signatures) == 0 {
		return nil, errors.New("no matching Wiki calls found")
	}
	return signatures, nil
}

func equalSignatureCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for signature, count := range left {
		if right[signature] != count {
			return false
		}
	}
	return true
}

func allCalls(input runInput) []modelCall {
	if len(input.ModelCalls) > 0 {
		return input.ModelCalls
	}
	return input.Calls
}

func readRun(path string) (runInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runInput{}, err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return runInput{}, err
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		data = envelope.Data
	}
	var run runInput
	if err := json.Unmarshal(data, &run); err != nil {
		return runInput{}, err
	}
	if run.Embedding.ProviderCalls < 0 || run.Embedding.RequestedTexts < 0 {
		return runInput{}, errors.New("embedding counters must be non-negative")
	}
	// Evaluation result files already contain task-scoped model calls. Derive
	// provider calls from them so users do not need a separate telemetry export.
	if run.Embedding.ProviderCalls == 0 {
		for _, call := range allCalls(run) {
			if strings.EqualFold(call.ModelType, "Embedding") {
				run.Embedding.ProviderCalls++
			}
		}
	}
	return run, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
