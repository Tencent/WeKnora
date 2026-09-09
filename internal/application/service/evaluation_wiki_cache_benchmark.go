package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const wikiCacheBenchmarkRepetitions = 3

type wikiCacheBenchmarkCollector struct {
	mu    sync.Mutex
	key   []byte
	calls []types.EvaluationEvidenceCall
}

func (c *wikiCacheBenchmarkCollector) ObserveLLMCall(observation types.LLMCallObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	observation.Error = ""
	c.calls = append(c.calls, types.EvaluationEvidenceCall{
		ID: uuid.NewString(), ModelID: observation.ModelID, ModelName: observation.ModelName,
		ModelType: observation.ModelType, Purpose: observation.Purpose,
		PromptPrefixFingerprint: protectModelCallFingerprint(observation.PromptPrefixFingerprint, c.key),
		RequestFingerprint:      protectModelCallFingerprint(observation.RequestFingerprint, c.key),
		Usage:                   observation.Usage, Pricing: observation.Pricing,
		EstimatedCost: observation.EstimatedCost, DurationMS: observation.DurationMS,
		Success: observation.Success, CreatedAt: time.Now().UTC(),
	})
}

func (c *wikiCacheBenchmarkCollector) snapshot() []types.EvaluationEvidenceCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]types.EvaluationEvidenceCall(nil), c.calls...)
}

type wikiCacheBenchmarkRequest struct {
	purpose  string
	messages []chat.Message
	options  *chat.ChatOptions
}

// WikiCacheBenchmark runs three isolated cold Wiki-shaped requests followed by
// byte-identical warm replays. It does not read or mutate Wiki pages.
func (e *EvaluationService) WikiCacheBenchmark(
	ctx context.Context,
	modelID string,
) (*types.WikiCacheBenchmarkEvidence, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, errors.New("chat model ID is required")
	}
	if len(e.evaluationStorage.fingerprintKey) == 0 {
		return nil, errors.New("WEKNORA_MODEL_CALL_FINGERPRINT_KEY must be configured")
	}
	model, err := e.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get benchmark chat model: %w", err)
	}
	benchmarkID := uuid.NewString()
	requests, workloadSHA, configSHA, err := buildWikiCacheBenchmarkRequests(
		types.MustTenantIDFromContext(ctx), benchmarkID, model,
	)
	if err != nil {
		return nil, err
	}
	report := &types.WikiCacheBenchmarkEvidence{
		SchemaVersion: 1, BenchmarkID: benchmarkID, GeneratedAt: time.Now().UTC(),
		CodeVersion: evaluationCodeVersion(), ModelID: model.GetModelID(), ModelName: model.GetModelName(),
		Repetitions: wikiCacheBenchmarkRepetitions, WorkloadSHA256: workloadSHA,
		ConfigurationSHA: configSHA,
	}

	runCohort := func() ([]types.EvaluationEvidenceCall, string) {
		collector := &wikiCacheBenchmarkCollector{key: e.evaluationStorage.fingerprintKey}
		for _, request := range requests {
			callCtx := types.WithLLMCallObserver(ctx, collector)
			callCtx = types.WithLLMCallMetadata(
				callCtx, request.purpose, chat.PromptPrefixFingerprint(request.messages, request.options),
			)
			if _, callErr := model.Chat(callCtx, request.messages, request.options); callErr != nil {
				return collector.snapshot(), callErr.Error()
			}
		}
		return collector.snapshot(), ""
	}

	coldCalls, coldErr := runCohort()
	warmCalls, warmErr := runCohort()
	report.Cold = summarizeWikiCacheBenchmarkCohort(coldCalls)
	report.Warm = summarizeWikiCacheBenchmarkCohort(warmCalls)
	if coldErr != "" {
		report.Warnings = append(report.Warnings, "cold_provider_error")
	}
	if warmErr != "" {
		report.Warnings = append(report.Warnings, "warm_provider_error")
	}
	report.StrictValidation = validateWikiCacheBenchmark(report)
	if err := sealWikiCacheBenchmarkEvidence(report); err != nil {
		return nil, err
	}
	return report, nil
}

func buildWikiCacheBenchmarkRequests(
	tenantID uint64,
	benchmarkID string,
	model chat.Chat,
) ([]wikiCacheBenchmarkRequest, string, string, error) {
	userTemplate, err := template.New("wiki-cache-benchmark").Parse(agent.WikiPageModifyUserPrompt)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse Wiki benchmark prompt: %w", err)
	}
	requests := make([]wikiCacheBenchmarkRequest, 0, wikiCacheBenchmarkRepetitions)
	workloadHasher := sha256.New()
	for index := 1; index <= wikiCacheBenchmarkRepetitions; index++ {
		scenario := fmt.Sprintf("scenario-%d", index)
		fact := fmt.Sprintf(
			"RHINO-CACHE-2026 controlled Wiki benchmark %d states that the evidence protocol uses identical cold and warm requests, records provider-reported cache tokens, and never stores prompt or response bodies.",
			index,
		)
		data := map[string]string{
			"HasAdditions": "1", "PageSlug": "rhino-cache-2026-" + scenario,
			"PageTitle": "RHINO-CACHE-2026 " + scenario, "PageType": "benchmark protocol",
			"ExistingContent": "(New page)", "SharedSourceContexts": strings.Repeat(fact+"\n", 6),
			"NewContent": strings.Repeat(fact+"\n", 12), "AvailableSlugs": "(none)", "Language": "zh-CN",
		}
		var userPrompt strings.Builder
		if err := userTemplate.Execute(&userPrompt, data); err != nil {
			return nil, "", "", fmt.Errorf("render Wiki benchmark prompt: %w", err)
		}
		// A per-scenario nonce prevents the three cold requests from warming one
		// another through the otherwise shared production Wiki system prefix.
		systemPrompt := fmt.Sprintf(
			"Controlled cache experiment %s %s. This marker isolates the cold prefix.\n\n%s",
			benchmarkID, scenario, agent.WikiPageModifySystemPrompt,
		)
		messages := []chat.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt.String()},
		}
		thinking := false
		purpose := "wiki_cache_benchmark_" + scenario
		prefix := chat.PromptPrefixFingerprint(messages, nil)
		options := &chat.ChatOptions{
			Temperature: 0, Seed: 2026 + index, Thinking: &thinking, MaxTokens: 96,
			PromptCacheKey: chat.BuildPromptCacheKey(tenantID, model.GetModelID(), purpose, prefix),
			CacheRetention: chat.CacheRetentionShort,
		}
		requestBytes, _ := json.Marshal(struct {
			Purpose  string            `json:"purpose"`
			Messages []chat.Message    `json:"messages"`
			Options  *chat.ChatOptions `json:"options"`
		}{purpose, messages, options})
		_, _ = workloadHasher.Write(requestBytes)
		requests = append(requests, wikiCacheBenchmarkRequest{purpose, messages, options})
	}
	configBytes, _ := json.Marshal(struct {
		ModelID     string `json:"model_id"`
		ModelName   string `json:"model_name"`
		Repetitions int    `json:"repetitions"`
		CodeVersion string `json:"code_version"`
	}{model.GetModelID(), model.GetModelName(), wikiCacheBenchmarkRepetitions, evaluationCodeVersion()})
	return requests,
		fmt.Sprintf("sha256:%x", workloadHasher.Sum(nil)),
		fmt.Sprintf("sha256:%x", sha256.Sum256(configBytes)), nil
}

func summarizeWikiCacheBenchmarkCohort(calls []types.EvaluationEvidenceCall) types.WikiCacheBenchmarkCohort {
	cohort := types.WikiCacheBenchmarkCohort{Calls: calls}
	latencies := make([]int64, 0, len(calls))
	for _, call := range calls {
		latencies = append(latencies, call.DurationMS)
		usage := &cohort.Usage
		usage.CallCount++
		if call.Success {
			usage.SuccessfulCalls++
		} else {
			usage.FailedCalls++
		}
		usage.PromptTokens += call.Usage.PromptTokens
		usage.CompletionTokens += call.Usage.CompletionTokens
		usage.TotalTokens += call.Usage.TotalTokens
		usage.CacheReadTokens += call.Usage.CacheReadTokens
		usage.CacheWriteTokens += call.Usage.CacheWriteTokens
		usage.CacheMissTokens += call.Usage.CacheMissTokens
		usage.ModelDurationMS += call.DurationMS
		if call.Usage.CacheReported {
			usage.CacheReportedCalls++
		}
		if call.Usage.CacheReadTokens > 0 {
			usage.CacheHitCalls++
		}
		if call.Pricing.Enabled {
			usage.PricedCalls++
			if usage.CostByCurrency == nil {
				usage.CostByCurrency = map[string]float64{}
			}
			usage.CostByCurrency[call.Pricing.Normalize().Currency] += call.EstimatedCost
		} else {
			usage.UnpricedCalls++
		}
	}
	if usage := &cohort.Usage; usage.CallCount > 0 {
		usage.AverageModelLatencyMS = float64(usage.ModelDurationMS) / float64(usage.CallCount)
		usage.CacheCoverageRate = float64(usage.CacheReportedCalls) / float64(usage.CallCount)
	}
	if cohort.Usage.PromptTokens > 0 {
		cohort.Usage.CacheHitRate = float64(cohort.Usage.CacheReadTokens) / float64(cohort.Usage.PromptTokens)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	cohort.MedianLatencyMS = nearestRankInt64(latencies, 0.5)
	cohort.P95LatencyMS = nearestRankInt64(latencies, 0.95)
	return cohort
}

func nearestRankInt64(values []int64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values))*quantile+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return float64(values[index])
}

func validateWikiCacheBenchmark(report *types.WikiCacheBenchmarkEvidence) types.WikiCacheStrictValidation {
	fail := func(reason string) types.WikiCacheStrictValidation {
		return types.WikiCacheStrictValidation{Reason: reason}
	}
	if len(report.Cold.Calls) != report.Repetitions || len(report.Warm.Calls) != report.Repetitions {
		return fail("cold and warm cohorts must both contain exactly three calls")
	}
	for index := range report.Cold.Calls {
		cold, warm := report.Cold.Calls[index], report.Warm.Calls[index]
		if !cold.Success || !warm.Success {
			return fail("every cold and warm call must succeed")
		}
		if cold.ModelID != warm.ModelID || cold.Purpose != warm.Purpose ||
			cold.RequestFingerprint == "" || cold.RequestFingerprint != warm.RequestFingerprint {
			return fail("model, purpose, and complete request fingerprint must match pairwise")
		}
		if !cold.Usage.CacheReported || !warm.Usage.CacheReported {
			return fail("provider cache telemetry is required for every call")
		}
	}
	if report.Cold.Usage.CacheReadTokens != 0 {
		return fail("cold cohort contains provider-reported cache hits")
	}
	if report.Warm.Usage.CacheReadTokens == 0 {
		return fail("warm cohort contains no provider-reported cache hits")
	}
	return types.WikiCacheStrictValidation{Passed: true}
}

func sealWikiCacheBenchmarkEvidence(report *types.WikiCacheBenchmarkEvidence) error {
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode Wiki cache benchmark evidence: %w", err)
	}
	report.ReportSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
	return nil
}
