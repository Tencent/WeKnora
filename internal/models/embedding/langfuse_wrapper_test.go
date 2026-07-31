package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

func TestPreparedEmbeddingObservabilityContainsOnlySafeSummary(t *testing.T) {
	const (
		executionHash = "1234567890abcdef"
		privateInput  = "private-query tenant-private"
		privateModel  = "private-model"
		privateID     = "private-model-id"
		privateError  = "private-provider-error"
	)
	ctx := langfuse.WithPreparedKnowledgeScope(
		context.Background(),
		executionHash,
	)
	options := buildLangfuseEmbeddingOptions(
		ctx,
		"embedding.embed",
		privateModel,
		privateID,
		[]string{privateInput},
		3,
		false,
	)
	output := buildLangfuseEmbeddingOutput(
		ctx,
		[][]float32{{0.123456, 0.234567, 0.345678}},
	)
	traceErr := buildLangfuseEmbeddingError(ctx, errors.New(privateError))
	debugRecord := preparedEmbeddingDebugRecord(
		executionHash[:12],
		[]string{privateInput},
		[][]float32{{0.123456, 0.234567, 0.345678}},
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
		t.Fatalf("marshal prepared embedding observability: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		privateInput,
		privateModel,
		privateID,
		privateError,
		"0.123456",
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf(
				"prepared embedding observability leaked %q: %s",
				secret,
				payload,
			)
		}
	}
	if !strings.Contains(payload, executionHash[:12]) {
		t.Fatalf(
			"prepared embedding observability omitted hash prefix: %s",
			payload,
		)
	}
}
