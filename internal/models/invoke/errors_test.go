package invoke

// errors_test.go — seam ① (P1c): ClassifyStatusBody must carry the extracted
// error.message out of a JSON vendor envelope (v1 go-openai APIError.Message /
// anthropic chatResp.Error.Message semantics) while Kind/Status stay decided
// by status + body patterns alone.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyStatusBodyExtractsErrorMessage(t *testing.T) {
	pe := ClassifyStatusBody(http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	assert.Equal(t, ErrAuth, pe.Kind)
	assert.Equal(t, http.StatusUnauthorized, pe.Status)
	assert.Equal(t, "Incorrect API key provided", pe.Message)
}

func TestClassifyStatusBodyRawBodyKeepsSnippet(t *testing.T) {
	// Non-envelope bodies (plain text) keep the raw snippet — v1 raw-HTTP
	// path semantics.
	pe := ClassifyStatusBody(http.StatusTooManyRequests, `rate limit exceeded`)
	assert.Equal(t, ErrRateLimited, pe.Kind)
	assert.Equal(t, http.StatusTooManyRequests, pe.Status)
	assert.Equal(t, "rate limit exceeded", pe.Message)
}

func TestClassifyStatusBodyTopLevelMessageFallback(t *testing.T) {
	// DashScope 原生错误信封：顶层 {code, message, request_id}，无 error 包装。
	pe := ClassifyStatusBody(http.StatusBadRequest,
		`{"code":"InvalidParameter","message":"The specified parameter is not valid.","request_id":"r-123"}`)
	assert.Equal(t, ErrProviderUpstream, pe.Kind)
	assert.Equal(t, http.StatusBadRequest, pe.Status)
	assert.Equal(t, "The specified parameter is not valid.", pe.Message)
	// error.message 优先于顶层 message（OpenAI 形信封不受影响）。
	pe = ClassifyStatusBody(http.StatusBadRequest,
		`{"error":{"message":"inner"},"message":"outer"}`)
	assert.Equal(t, "inner", pe.Message)
}

func TestClassifyStatusBodyEnvelopeStillClassifiesOnBody(t *testing.T) {
	// Classification patterns still run on the FULL body even when the
	// surfaced message is extracted.
	pe := ClassifyStatusBody(http.StatusBadRequest,
		`{"error":{"message":"short","type":"invalid_request_error"},"code":"context_length_exceeded"}`)
	assert.Equal(t, ErrContextExceeded, pe.Kind)
	assert.Equal(t, "short", pe.Message)
}

func TestWrapInvokeErrorPrefixesActionOnProviderError(t *testing.T) {
	base := ClassifyStatusBody(http.StatusUnauthorized, `{"error":{"message":"bad key"}}`)
	wrapped := WrapInvokeError("create chat completion", base)
	assert.Contains(t, wrapped.Error(), "create chat completion: bad key")
}

// TestWrapInvokeErrorNoUnwrapCycle pins the P0 fix (2026-09-13 review): the
// executor's errors are already *ProviderError — re-wrapping must not point
// Unwrap() back at the instance itself, which hangs any errors.Is/As chain
// walk (asynq's SkipRetry check walks exactly such a chain on the worker
// side and would wedge the goroutine forever).
func TestWrapInvokeErrorNoUnwrapCycle(t *testing.T) {
	base := &ProviderError{Kind: ErrProviderUpstream, Status: 400, Message: "boom"}
	wrapped := WrapInvokeError("create chat completion", base)
	assert.Same(t, base, wrapped, "顶层 ProviderError 原地加前缀，不回写 Err")
	assert.Contains(t, wrapped.Error(), "create chat completion: boom")
	assert.False(t, errors.Is(wrapped, context.DeadlineExceeded), "链走查必须终止（无自引用环）")

	// Non-ProviderError chains keep the wrap-through behavior: the classified
	// instance carries the original error for unwrapping.
	deadline := fmt.Errorf("do: %w", context.DeadlineExceeded)
	wrapped2 := WrapInvokeError("create chat completion", deadline)
	assert.ErrorIs(t, wrapped2, context.DeadlineExceeded)
	assert.Equal(t, ErrTimeout, ClassifyError(wrapped2).Kind)
}
