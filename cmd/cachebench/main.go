package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type runInput struct {
	Embedding  embeddingRun `json:"embedding"`
	ModelCalls []modelCall  `json:"model_calls"`
	Calls      []modelCall  `json:"calls"`
}

type embeddingRun struct {
	RequestedTexts int `json:"requested_texts"`
	ProviderCalls  int `json:"provider_calls"`
}

type modelCall struct {
	ModelType     string  `json:"model_type"`
	Purpose       string  `json:"purpose"`
	Usage         usage   `json:"usage"`
	Pricing       pricing `json:"pricing"`
	EstimatedCost float64 `json:"estimated_cost"`
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
	CostByCurrency     map[string]float64 `json:"cost_by_currency"`
}

type comparisonReport struct {
	PurposePrefix string `json:"purpose_prefix"`
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
	report := compareRuns(before, after, *purposePrefix)
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
	summary := wikiSummary{CostByCurrency: make(map[string]float64)}
	for _, call := range calls {
		if !strings.HasPrefix(call.Purpose, purposePrefix) {
			continue
		}
		summary.CallCount++
		summary.PromptTokens += call.Usage.PromptTokens
		summary.CacheReadTokens += call.Usage.CacheReadTokens
		summary.CacheWriteTokens += call.Usage.CacheWriteTokens
		summary.CacheMissTokens += call.Usage.CacheMissTokens
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
	return summary
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
