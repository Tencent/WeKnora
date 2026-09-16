package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareRuns(t *testing.T) {
	before := runInput{
		Embedding: embeddingRun{RequestedTexts: 100, ProviderCalls: 100},
		ModelCalls: []modelCall{
			{Purpose: "wiki_page_modify", Usage: usage{PromptTokens: 100, CacheMissTokens: 100, CacheReported: true}, Pricing: pricing{Currency: "usd"}, EstimatedCost: 0.2},
			{Purpose: "knowledge_qa", Usage: usage{PromptTokens: 1000}},
		},
	}
	after := runInput{
		Embedding: embeddingRun{RequestedTexts: 100, ProviderCalls: 25},
		ModelCalls: []modelCall{
			{Purpose: "wiki_page_modify", Usage: usage{PromptTokens: 100, CacheReadTokens: 80, CacheMissTokens: 20, CacheReported: true}, Pricing: pricing{Currency: "USD"}, EstimatedCost: 0.08},
		},
	}

	report := compareRuns(before, after, "wiki_")
	require.Equal(t, 75, report.Embedding.CallsSaved)
	require.InDelta(t, 0.75, report.Embedding.ReductionRate, 0.0001)
	require.Equal(t, 1, report.Wiki.Before.CallCount)
	require.InDelta(t, 0.8, report.Wiki.After.CacheHitRate, 0.0001)
	require.InDelta(t, 0.8, report.Wiki.CacheHitRateDelta, 0.0001)
	require.InDelta(t, 0.08, report.Wiki.After.CostByCurrency["USD"], 0.0001)
}

func TestReadRunDerivesEmbeddingProviderCallsFromEvaluationResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evaluation.json")
	data := []byte(`{"data":{"model_calls":[` +
		`{"model_type":"Embedding","purpose":"embedding"},` +
		`{"model_type":"KnowledgeQA","purpose":"wiki_page_modify"},` +
		`{"model_type":"Embedding","purpose":"embedding"}` +
		`]}}`)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	run, err := readRun(path)
	require.NoError(t, err)
	require.Equal(t, 2, run.Embedding.ProviderCalls)
}

func TestSummarizeCallsUsesCallsFallback(t *testing.T) {
	input := runInput{Calls: []modelCall{{
		Purpose: "wiki_summary", DurationMS: 100,
		Usage: usage{PromptTokens: 100, CacheReadTokens: 30, CacheMissTokens: 70, CacheReported: true},
	}}}
	summary := summarizeCalls(allCalls(input), "wiki_")
	require.Equal(t, 1, summary.CallCount)
	require.InDelta(t, 0.3, summary.CacheHitRate, 0.0001)
	require.InDelta(t, 100, summary.MedianLatencyMS, 0.0001)
	require.InDelta(t, 100, summary.P95LatencyMS, 0.0001)
}

func TestSummarizeCallsReportsMedianP95AndNormalizedCost(t *testing.T) {
	calls := make([]modelCall, 0, 20)
	for i := 1; i <= 20; i++ {
		calls = append(calls, modelCall{
			Purpose: "wiki_page", DurationMS: int64(i * 100), EstimatedCost: 0.01,
			Usage: usage{PromptTokens: 100}, Pricing: pricing{Currency: "cny"},
		})
	}
	summary := summarizeCalls(calls, "wiki_")
	require.InDelta(t, 1050, summary.MedianLatencyMS, 0.0001)
	require.InDelta(t, 1900, summary.P95LatencyMS, 0.0001)
	require.InDelta(t, 0.1, summary.CostPerKPrompt["CNY"], 0.0001)
}

func TestValidateStrictPairAcceptsMatchedRepeatedCohorts(t *testing.T) {
	before, after := matchedStrictRuns()
	require.NoError(t, validateStrictPair(before, after, "wiki_"))
}

func TestValidateStrictPairRejectsUnmatchedWorkload(t *testing.T) {
	before, after := matchedStrictRuns()
	after.Experiment.WorkloadFingerprint = "sha256:different"
	err := validateStrictPair(before, after, "wiki_")
	require.EqualError(t, err, "workload_fingerprint must be non-empty and identical")
}

func TestValidateStrictPairRejectsDifferentRequestFingerprints(t *testing.T) {
	before, after := matchedStrictRuns()
	after.ModelCalls[0].RequestFingerprint = "hmac:different"
	err := validateStrictPair(before, after, "wiki_")
	require.EqualError(t, err, "Wiki call count or request signatures differ between cohorts")
}

func TestValidateStrictPairRejectsRepeatedColdSignature(t *testing.T) {
	before, after := matchedStrictRuns()
	before.ModelCalls[1].RequestFingerprint = before.ModelCalls[0].RequestFingerprint
	after.ModelCalls[1].RequestFingerprint = after.ModelCalls[0].RequestFingerprint
	err := validateStrictPair(before, after, "wiki_")
	require.EqualError(t, err, "repetitions must equal the number of distinct matched Wiki call signatures")
}

func TestValidateStrictPairRejectsWarmOrFailedColdCohort(t *testing.T) {
	before, after := matchedStrictRuns()
	before.ModelCalls[0].Usage.CacheReadTokens = 10
	before.ModelCalls[0].Usage.CacheMissTokens = 90
	err := validateStrictPair(before, after, "wiki_")
	require.EqualError(t, err, "cold cohort contains cache hits")

	before, after = matchedStrictRuns()
	before.ModelCalls[0].Success = false
	err = validateStrictPair(before, after, "wiki_")
	require.EqualError(t, err, `cold cohort: call for purpose "wiki_page" was not successful`)
}

func matchedStrictRuns() (runInput, runInput) {
	protocol := experimentProtocol{
		ProtocolVersion: 1, WorkloadFingerprint: "sha256:workload", ModelFingerprint: "sha256:model",
		ConfigurationFingerprint: "sha256:config", Repetitions: 3,
	}
	before := runInput{
		Experiment: protocol,
		ModelCalls: []modelCall{
			{ModelID: "model-1", Purpose: "wiki_page", RequestFingerprint: "hmac:request-1", Success: true, DurationMS: 100, Usage: usage{PromptTokens: 100, CacheMissTokens: 100, CacheReported: true}},
			{ModelID: "model-1", Purpose: "wiki_page", RequestFingerprint: "hmac:request-2", Success: true, DurationMS: 120, Usage: usage{PromptTokens: 100, CacheMissTokens: 100, CacheReported: true}},
			{ModelID: "model-1", Purpose: "wiki_page", RequestFingerprint: "hmac:request-3", Success: true, DurationMS: 140, Usage: usage{PromptTokens: 100, CacheMissTokens: 100, CacheReported: true}},
		},
	}
	before.Experiment.Cohort = "cold"
	after := before
	after.Experiment.Cohort = "warm"
	after.ModelCalls = append([]modelCall(nil), before.ModelCalls...)
	after.ModelCalls[0].Usage = usage{PromptTokens: 100, CacheReadTokens: 80, CacheMissTokens: 20, CacheReported: true}
	return before, after
}
