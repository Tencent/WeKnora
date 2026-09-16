// Package call connects physical provider attempts to the application ledger.
package call

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// Finish persists a provider attempt's terminal outcome and returns its effective error.
type Finish func(error, *types.TokenUsage) error

// Observer starts accounting immediately before a physical provider request.
type (
	Observer    func(context.Context, string) (Finish, error)
	observerKey struct{}
)

// WithObserver installs request accounting for this context.
func WithObserver(ctx context.Context, observer Observer) context.Context {
	return context.WithValue(ctx, observerKey{}, observer)
}

// WithMetadata copies and extends the request metadata without mutating its parent.
func WithMetadata(ctx context.Context, metadata map[string]any) context.Context {
	merged := make(map[string]any)
	for key, value := range Metadata(ctx) {
		merged[key] = value
	}
	for key, value := range metadata {
		merged[key] = value
	}
	return context.WithValue(ctx, types.ModelRequestMetadataContextKey, merged)
}

// Metadata returns the request metadata attached to this context.
func Metadata(ctx context.Context) map[string]any {
	metadata, _ := ctx.Value(types.ModelRequestMetadataContextKey).(map[string]any)
	return metadata
}

// Start is called immediately before an actual provider request, inside batches and retries.
func Start(ctx context.Context, operation string) (Finish, error) {
	if err := types.ModelAccountingError(ctx); err != nil {
		return nil, err
	}
	if observer, ok := ctx.Value(observerKey{}).(Observer); ok {
		return observer(ctx, operation)
	}
	return func(err error, _ *types.TokenUsage) error { return err }, nil
}

// DoJSON records one HTTP attempt and returns a replayable response body. Responses
// are bounded; only usage counters, never response contents, enter the ledger.
func DoJSON(client *http.Client, req *http.Request, operation string) (*http.Response, error) {
	finish, err := Start(req.Context(), operation)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, finish(err, nil)
	}
	const maxBody = 64 << 20
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	closeErr := resp.Body.Close()
	if readErr == nil {
		readErr = closeErr
	}
	if len(body) > maxBody {
		readErr = errors.New("provider response exceeds 64 MiB")
	}
	providerErr := readErr
	if providerErr == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		providerErr = fmt.Errorf("provider HTTP status %d", resp.StatusCode)
	}
	if providerErr == nil && !json.Valid(body) {
		providerErr = errors.New("provider returned invalid JSON")
	}
	finishErr := finish(providerErr, ParseJSONUsage(body))
	if errors.Is(finishErr, types.ErrModelAccounting) {
		return nil, finishErr
	}
	if readErr != nil {
		return nil, finishErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// ParseJSONUsage preserves usage presence even when every counter is zero.
// Unrecognized provider schemas remain unavailable.
func ParseJSONUsage(body []byte) *types.TokenUsage {
	var raw struct {
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			InputTokens      *int `json:"input_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			OutputTokens     *int `json:"output_tokens"`
			TotalTokens      *int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Usage == nil {
		return nil
	}
	u := raw.Usage
	if u.PromptTokens == nil && u.InputTokens == nil && u.TotalTokens == nil {
		return nil
	}
	value := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	prompt := value(u.PromptTokens)
	if u.InputTokens != nil {
		prompt = *u.InputTokens
	}
	completion := value(u.CompletionTokens)
	if u.OutputTokens != nil {
		completion = *u.OutputTokens
	}
	total := prompt + completion
	if u.TotalTokens != nil {
		total = *u.TotalTokens
		if u.PromptTokens == nil && u.InputTokens == nil {
			prompt = total - completion
		}
	}
	result := &types.TokenUsage{
		UsageReported: true, PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total,
	}
	types.CaptureReportedCost(body, result)
	return result
}

// WithNewBatch assigns a fresh logical batch identity to the first request attempt.
func WithNewBatch(ctx context.Context) context.Context {
	return WithMetadata(ctx, map[string]any{"batch_id": uuid.NewString(), "attempt_number": 1})
}
