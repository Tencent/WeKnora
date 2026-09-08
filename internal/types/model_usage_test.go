package types

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

func validModelUsage() *ModelUsage {
	return &ModelUsage{
		TenantID: 1, ModelTenantID: 10000,
		ModelID: "chat-safe", ModelName: "gpt-safe", ModelType: string(ModelTypeKnowledgeQA),
		ModelSource: string(ModelSourceOpenAI), ResolvedProvider: "openai",
		CallType: CallTypeChat, Purpose: "evaluation", Status: UsageStatusSuccess,
		TokenProvenance: TokenProvenanceProviderReported,
	}
}

func TestModelUsageSecretExclusion(t *testing.T) {
	data, err := json.Marshal(validModelUsage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"api_key", "app_secret", "authorization", "custom_headers",
		"prompt_text", "query_text", "completion_text", "content",
		"raw_request", "raw_response", "request_body", "response_body",
		"error_message", "password",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("ModelUsage JSON must not expose %q: %s", forbidden, string(data))
		}
	}
}

func TestModelUsageValidateAcceptsValidRow(t *testing.T) {
	usage := validModelUsage()
	usage.LogicalRequests = 1
	usage.InputTokens = intPtr(100)
	usage.OutputTokens = intPtr(50)
	usage.TotalTokens = intPtr(150)
	if err := usage.Validate(); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}
}

func TestModelUsageValidateRejectsBadIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ModelUsage)
	}{
		{"zero tenant", func(u *ModelUsage) { u.TenantID = 0 }},
		{"zero model tenant", func(u *ModelUsage) { u.ModelTenantID = 0 }},
		{"empty model id", func(u *ModelUsage) { u.ModelID = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := validModelUsage()
			c.mutate(u)
			if err := u.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestModelUsageValidateRejectsBadEnums(t *testing.T) {
	badCallType := CallType("bogus")
	badStatus := UsageStatus("bogus")
	badProvenance := TokenProvenance("bogus")
	badPrompt := PromptCacheStatus("bogus")
	badEmbedding := EmbeddingCacheStatus("bogus")

	cases := []struct {
		name   string
		mutate func(*ModelUsage)
	}{
		{"call_type", func(u *ModelUsage) { u.CallType = badCallType }},
		{"status", func(u *ModelUsage) { u.Status = badStatus }},
		{"token_provenance", func(u *ModelUsage) { u.TokenProvenance = badProvenance }},
		{"prompt_cache_status", func(u *ModelUsage) { u.PromptCacheStatus = &badPrompt }},
		{"embedding_cache_status", func(u *ModelUsage) { u.EmbeddingCacheStatus = &badEmbedding }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := validModelUsage()
			c.mutate(u)
			if err := u.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestModelUsageValidateRejectsNegativeNumbers(t *testing.T) {
	negToken := -1
	cases := []struct {
		name   string
		mutate func(*ModelUsage)
	}{
		{"input_tokens", func(u *ModelUsage) { u.InputTokens = &negToken }},
		{"cache_read_tokens", func(u *ModelUsage) { u.CacheReadTokens = &negToken }},
		{"logical_requests", func(u *ModelUsage) { u.LogicalRequests = -1 }},
		{"provider_pairs", func(u *ModelUsage) { u.ProviderPairs = -1 }},
		{"latency_ms", func(u *ModelUsage) { l := int64(-1); u.LatencyMS = &l }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := validModelUsage()
			c.mutate(u)
			if err := u.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestModelUsageValidateEmbeddingCacheAccounting(t *testing.T) {
	status := EmbeddingCacheStatusPartial
	base := func() *ModelUsage {
		u := validModelUsage()
		u.CallType = CallTypeEmbedding
		u.ModelType = string(ModelTypeEmbedding)
		u.EmbeddingCacheStatus = &status
		u.EmbeddingInputs = 10
		u.CacheHits = 3
		u.CacheMisses = 7
		return u
	}

	if err := base().Validate(); err != nil {
		t.Fatalf("consistent cache accounting rejected: %v", err)
	}

	broken := base()
	broken.CacheMisses = 6 // 3 + 6 != 10
	if err := broken.Validate(); err == nil {
		t.Fatal("expected cache accounting mismatch to be rejected")
	}

	// Disabled cache must not be held to the accounting identity: the counters
	// are zero regardless of input count.
	disabled := EmbeddingCacheStatusDisabled
	off := base()
	off.EmbeddingCacheStatus = &disabled
	off.CacheHits = 0
	off.CacheMisses = 0
	if err := off.Validate(); err != nil {
		t.Fatalf("disabled cache must skip accounting identity: %v", err)
	}

	// Unrecorded cache outcome must not be asserted either.
	nilStatus := base()
	nilStatus.EmbeddingCacheStatus = nil
	if err := nilStatus.Validate(); err != nil {
		t.Fatalf("unrecorded cache outcome must skip accounting identity: %v", err)
	}
}

func TestWithEvaluationRunIDRoundTrip(t *testing.T) {
	ctx := WithEvaluationRunID(context.Background(), "run-123")
	got, ok := EvaluationRunIDFromContext(ctx)
	if !ok || got != "run-123" {
		t.Fatalf("EvaluationRunIDFromContext = (%q, %v), want (%q, true)", got, ok, "run-123")
	}
}

func TestEvaluationRunIDAbsentFromOrdinaryContext(t *testing.T) {
	if got, ok := EvaluationRunIDFromContext(context.Background()); ok || got != "" {
		t.Fatalf("ordinary context must carry no run ID, got (%q, %v)", got, ok)
	}
}

func TestWithEvaluationRunIDEmptyIsNoOp(t *testing.T) {
	base := WithEvaluationRunID(context.Background(), "run-keep")
	ctx := WithEvaluationRunID(base, "")
	if got, ok := EvaluationRunIDFromContext(ctx); !ok || got != "run-keep" {
		t.Fatalf("empty run ID must not overwrite an existing one, got (%q, %v)", got, ok)
	}

	ctx = WithEvaluationRunID(context.Background(), "   ")
	if _, ok := EvaluationRunIDFromContext(ctx); ok {
		t.Fatal("whitespace run ID must be treated as absent")
	}
}

func TestEvaluationRunIDSurvivesCloneDecision(t *testing.T) {
	clone, declared := ContextCloneDecision(EvaluationRunIDContextKey)
	if !declared {
		t.Fatal("EvaluationRunIDContextKey has no clone decision")
	}
	if !clone {
		t.Fatal(
			"EvaluationRunIDContextKey must survive logger.CloneContext " +
				"so sync evaluation attribution is not dropped",
		)
	}

	found := false
	for _, key := range ContextKeysClonedAcrossDetach() {
		if key == EvaluationRunIDContextKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("EvaluationRunIDContextKey missing from ContextKeysClonedAcrossDetach")
	}
}
