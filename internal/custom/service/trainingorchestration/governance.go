package trainingorchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const defaultCompletionCallLimit = 256

// CompletionGate is the shared safety boundary for every rendered model
// prompt. It only records prompt metadata; it never records prompt content.
type CompletionGate struct {
	config CompletionGateConfig
	slots  chan struct{}

	mu          sync.Mutex
	calls       int
	inputTokens int
}

type CompletionGateConfig struct {
	RequestTimeout      time.Duration
	MaxConcurrent       int
	MaxTotalCalls       int
	MaxTotalInputTokens int
	Logger              func(CompletionLog)
}

type CompletionLog struct {
	Stage           string
	Batch           string
	Model           string
	Attempt         int
	CallNumber      int
	Budget          int
	EstimatedTokens int
	Duration        time.Duration
	EndReason       string
}

func NewCompletionGate(config CompletionGateConfig) *CompletionGate {
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 1
	}
	if config.MaxTotalCalls <= 0 {
		config.MaxTotalCalls = defaultCompletionCallLimit
	}
	if config.Logger == nil {
		config.Logger = func(entry CompletionLog) {
			slog.Info("training orchestration model call",
				"stage", entry.Stage,
				"batch", entry.Batch,
				"model", entry.Model,
				"attempt", entry.Attempt,
				"call_number", entry.CallNumber,
				"budget", entry.Budget,
				"estimated_tokens", entry.EstimatedTokens,
				"duration_ms", entry.Duration.Milliseconds(),
				"end_reason", entry.EndReason,
			)
		}
	}
	return &CompletionGate{
		config: config,
		slots:  make(chan struct{}, config.MaxConcurrent),
	}
}

func (g *CompletionGate) Reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = 0
	g.inputTokens = 0
}

// NewRun returns an empty gate with the same limits and logger so counters do
// not leak from one orchestration run into the next.
func (g *CompletionGate) NewRun() *CompletionGate {
	if g == nil {
		return nil
	}
	return NewCompletionGate(g.config)
}

func (g *CompletionGate) Complete(ctx context.Context, client CompletionClient, stage, batch, prompt string, inputLimit int, remaining *int) (string, error) {
	if g == nil {
		g = NewCompletionGate(CompletionGateConfig{})
	}
	if client == nil {
		return "", fmt.Errorf("training orchestration completion client is not configured")
	}
	estimated, err := estimateTokens(prompt)
	if err != nil {
		return "", err
	}
	if inputLimit > 0 && estimated > inputLimit {
		return "", &InputCapacityError{Tokens: estimated, Limit: inputLimit}
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := g.acquire(ctx); err != nil {
			return "", err
		}
		callNumber, reserveErr := g.reserve(estimated, inputLimit, remaining)
		if reserveErr != nil {
			g.release()
			return "", reserveErr
		}

		started := time.Now()
		requestCtx := ctx
		cancel := func() {}
		if g.config.RequestTimeout > 0 {
			requestCtx, cancel = context.WithTimeout(ctx, g.config.RequestTimeout)
		}
		raw, callErr := completeWithClient(requestCtx, client, prompt)
		cancel()
		g.release()

		endReason := completionEndReason(callErr)
		g.config.Logger(CompletionLog{
			Stage: stage, Batch: batch, Model: client.Model(), Attempt: attempt,
			CallNumber: callNumber, Budget: inputLimit, EstimatedTokens: estimated,
			Duration: time.Since(started), EndReason: endReason,
		})
		if callErr == nil {
			return raw, nil
		}
		if attempt == 1 && retryableCompletionError(callErr) {
			continue
		}
		return "", callErr
	}
	return "", fmt.Errorf("training orchestration completion exhausted retries")
}

func (g *CompletionGate) acquire(ctx context.Context) error {
	select {
	case g.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *CompletionGate) release() {
	<-g.slots
}

func (g *CompletionGate) reserve(estimated, inputLimit int, remaining *int) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.config.MaxTotalCalls > 0 && g.calls >= g.config.MaxTotalCalls {
		return 0, &GenerationError{Code: "call_limit_exceeded", Err: fmt.Errorf("training orchestration model calls exceed %d", g.config.MaxTotalCalls)}
	}
	if g.config.MaxTotalInputTokens > 0 && g.inputTokens+estimated > g.config.MaxTotalInputTokens {
		return 0, &InputCapacityError{Tokens: g.inputTokens + estimated, Limit: g.config.MaxTotalInputTokens}
	}
	if remaining != nil && estimated > *remaining {
		limit := *remaining
		if inputLimit > 0 && inputLimit < limit {
			limit = inputLimit
		}
		return 0, &InputCapacityError{Tokens: estimated, Limit: limit}
	}
	g.calls++
	g.inputTokens += estimated
	if remaining != nil {
		*remaining -= estimated
	}
	return g.calls, nil
}

func completeWithClient(ctx context.Context, client CompletionClient, prompt string) (string, error) {
	if structured, ok := client.(structuredCompletionClient); ok {
		return structured.CompleteJSON(ctx, prompt)
	}
	if streaming, ok := client.(streamingCompletionClient); ok {
		return streaming.Stream(ctx, prompt, nil)
	}
	return client.Complete(ctx, prompt)
}

func retryableCompletionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var incomplete interface{ IncompleteOutput() bool }
	if errors.As(err, &incomplete) && incomplete.IncompleteOutput() {
		return false
	}
	var closed interface{ ConnectionClosed() bool }
	if errors.As(err, &closed) && closed.ConnectionClosed() {
		return false
	}
	var streamTimeout interface{ StreamTimeoutPhase() string }
	if errors.As(err, &streamTimeout) {
		return false
	}
	var temporary interface{ Temporary() bool }
	return errors.As(err, &temporary) && temporary.Temporary()
}

func completionEndReason(err error) string {
	if err == nil {
		return "completed"
	}
	var incomplete interface{ IncompleteOutput() bool }
	if errors.As(err, &incomplete) && incomplete.IncompleteOutput() {
		return "truncated"
	}
	var closed interface{ ConnectionClosed() bool }
	if errors.As(err, &closed) && closed.ConnectionClosed() {
		return "truncated"
	}
	var streamTimeout interface{ StreamTimeoutPhase() string }
	if errors.As(err, &streamTimeout) {
		return "timeout"
	}
	if isProviderContextExceeded(err) {
		return "provider_context_exceeded"
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return "transport_error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "failed"
}

func classifyCompletionError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	var incomplete interface{ IncompleteOutput() bool }
	if errors.As(err, &incomplete) && incomplete.IncompleteOutput() || isIncompleteJSONError(err) {
		return &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	var closed interface{ ConnectionClosed() bool }
	if errors.As(err, &closed) && closed.ConnectionClosed() {
		return &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	var streamTimeout interface{ StreamTimeoutPhase() string }
	if errors.As(err, &streamTimeout) {
		return &GenerationError{Code: "timeout", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	if isProviderContextExceeded(err) {
		return &GenerationError{Code: "model_context_exceeded", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &GenerationError{Code: "timeout", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return &GenerationError{Code: "model_transport_failed", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	var invalid interface{ InvalidOutput() bool }
	if errors.As(err, &invalid) && invalid.InvalidOutput() {
		return &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("%s: %w", prefix, err)}
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func isInvalidStructuredOutput(err error) bool {
	if err == nil {
		return false
	}
	var invalid interface{ InvalidOutput() bool }
	return errors.As(err, &invalid) && invalid.InvalidOutput()
}

func isIncompleteJSONError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"unexpected end of json input",
		"unexpected end of json",
		"unexpected eof",
		"unexpected end of input",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isShrinkableGenerationError(err error) bool {
	if err == nil {
		return false
	}
	var generationErr *GenerationError
	if errors.As(err, &generationErr) {
		return generationErr.Code == "model_output_truncated" ||
			generationErr.Code == "model_output_invalid" ||
			generationErr.Code == "planning_output_invalid" ||
			generationErr.Code == "model_context_exceeded"
	}
	return false
}

// safeTaskErrorMessage keeps provider response bodies, prompt text, reasoning
// output and credentials out of persisted job records. The API already
// exposes the structured error code separately.
func safeTaskErrorMessage(code string, err error) string {
	suffix := ""
	if err != nil {
		const marker = "output_bytes="
		message := err.Error()
		if offset := strings.Index(message, marker); offset >= 0 {
			start := offset + len(marker)
			end := start
			for end < len(message) && message[end] >= '0' && message[end] <= '9' {
				end++
			}
			if end > start {
				suffix = " (output_bytes=" + message[start:end] + ")"
			}
		}
		if reason := safeErrorReason(err); reason != "" {
			suffix += " (reason=" + reason + ")"
		}
	}
	if strings.TrimSpace(code) != "" {
		return "training orchestration task failed: " + strings.TrimSpace(code) + suffix
	}
	if err == nil {
		return "training orchestration task failed"
	}
	return "training orchestration task failed"
}

// safeErrorReason maps internal validation failures to a small allowlist of
// diagnostics suitable for persisted task records. It deliberately excludes
// provider text, prompts, model output and identifiers.
func safeErrorReason(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "sources changed before generation started"):
		return "source_not_stable"
	case strings.Contains(message, "outside the current input whitelist"),
		strings.Contains(message, "outside orchestration profile"),
		strings.Contains(message, "outside the material whitelist"),
		strings.Contains(message, "outside its endpoint cluster"):
		return "evidence_reference_outside_whitelist"
	case strings.Contains(message, "unknown field"):
		return "json_unknown_field"
	case strings.Contains(message, "trailing content"):
		return "json_trailing_content"
	case strings.Contains(message, "arrays must be json arrays"),
		strings.Contains(message, "must be a json array"):
		return "json_array_required"
	case strings.Contains(message, "unexpected end of json"),
		strings.Contains(message, "unexpected eof"),
		strings.Contains(message, "output token limit reached"),
		strings.Contains(message, "output ended before closing"):
		return "json_output_truncated"
	case strings.Contains(message, "invalid character"),
		strings.Contains(message, "cannot unmarshal"),
		strings.Contains(message, "decode training orchestration model output"):
		return "json_decode_failed"
	case strings.Contains(message, "reject training orchestration model output"),
		strings.Contains(message, "planning output invalid"),
		strings.Contains(message, "planning merge returned no clusters"):
		return "business_contract_rejected"
	case strings.Contains(message, "required_before relations contain a cycle"):
		return "relation_cycle"
	case strings.Contains(message, "projection exceeds the first-release output capacity"):
		return "projection_capacity_exceeded"
	case strings.Contains(message, "topic_clusters["):
		return "topic_cluster_validation_rejected"
	case strings.Contains(message, "topic_cluster_relations["):
		return "relation_validation_rejected"
	case strings.Contains(message, "projection statistics do not match"):
		return "projection_statistics_mismatch"
	case strings.Contains(message, "relation input cluster"),
		strings.Contains(message, "relation generation"),
		strings.Contains(message, "stage-four relation generation"):
		return "relation_contract_rejected"
	case strings.Contains(message, "stage-four assembly"),
		strings.Contains(message, "validate assembled stage-four projection"):
		return "assembled_projection_rejected"
	default:
		return ""
	}
}

func isProviderContextExceeded(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context length",
		"context window",
		"maximum context",
		"prompt is too long",
		"too many tokens",
		"max input tokens",
		"input token limit",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
