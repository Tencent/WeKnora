package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

func TestPreparedRerankObservabilityContainsOnlySafeSummary(t *testing.T) {
	const (
		executionHash = "1234567890abcdef"
		privateQuery  = "private-query tenant-private"
		privateDoc    = "private-document knowledge-private"
		privateModel  = "private-model"
		privateID     = "private-model-id"
		privateError  = "private-provider-error"
	)
	ctx := langfuse.WithPreparedKnowledgeScope(
		context.Background(),
		executionHash,
	)
	results := []RankResult{{
		Index:          0,
		Document:       DocumentInfo{Text: privateDoc},
		RelevanceScore: 0.8,
	}}
	options := buildLangfuseRerankOptions(
		ctx,
		privateModel,
		privateID,
		privateQuery,
		[]string{privateDoc},
		len([]rune(privateQuery))+len([]rune(privateDoc)),
	)
	output := buildLangfuseRerankOutput(ctx, results, []string{privateDoc})
	traceErr := buildLangfuseRerankError(ctx, errors.New(privateError))
	debugRecord := preparedRerankDebugRecord(
		executionHash[:12],
		privateQuery,
		[]string{privateDoc},
		results,
		errors.New(privateError),
		0,
	)

	encoded, err := json.Marshal([]interface{}{
		options,
		output,
		traceErr.Error(),
		debugRecord,
	})
	if err != nil {
		t.Fatalf("marshal prepared rerank observability: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		privateQuery,
		privateDoc,
		privateModel,
		privateID,
		privateError,
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf("prepared rerank observability leaked %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, executionHash[:12]) {
		t.Fatalf("prepared rerank observability omitted hash prefix: %s", payload)
	}
}
