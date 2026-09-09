package chat

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ErrorKind is the unified provider-failure taxonomy (design §4.6). Retry and
// the future fallback chain decide on the kind alone — never on raw provider
// error strings, which drift across vendors.
type ErrorKind string

// ErrorKind values — the unified provider-failure taxonomy.
const (
	ErrAuth             ErrorKind = "auth"
	ErrRateLimited      ErrorKind = "rate_limited"
	ErrContextExceeded  ErrorKind = "context_exceeded"
	ErrTimeout          ErrorKind = "timeout"
	ErrContentPolicy    ErrorKind = "content_policy"
	ErrProviderUpstream ErrorKind = "provider_upstream"
)

// ProviderError is the unified translation of a provider failure. Provider
// response errors MUST surface as one (强制, design §4.6) so upstream
// consumers (asynq retries, the future fallback chain) classify by Kind.
type ProviderError struct {
	Kind    ErrorKind
	Status  int // HTTP status; 0 for network-level failures
	Message string
	Err     error
}

func (e *ProviderError) Error() string {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Status > 0 {
		return fmt.Sprintf("provider error [%s] status %d: %s", e.Kind, e.Status, msg)
	}
	return fmt.Sprintf("provider error [%s]: %s", e.Kind, msg)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// IsRetryable reports whether the failure is worth retrying (design §4.6):
// rate limits, upstream 5xx, and network-level failures retry; auth,
// context-exceeded, content-policy, and other 4xx fail identically every
// time and fail fast.
func (e *ProviderError) IsRetryable() bool {
	switch e.Kind {
	case ErrRateLimited, ErrTimeout:
		return true
	case ErrProviderUpstream:
		return e.Status == 0 || e.Status >= 500
	default:
		return false
	}
}

// contextOverflowPatterns are the cross-vendor phrasings of "prompt exceeded
// the model's context window". Mirrors the detection set in
// internal/agent/compaction/overflow.go (chat cannot import agent; the
// compaction package can later migrate to checking ErrContextExceeded kind).
var contextOverflowPatterns = regexp.MustCompile(strings.Join([]string{
	`(?i)too large for model with \d+ maximum context length`, // Mistral
	`(?i)but the configured context size is`,                  // DeepSeek
	`(?i)model_context_window_exceeded`,                       // z.ai
	`(?i)prompt too long; exceeded (max )?context length`,     // Ollama
	`(?i)range of input length should be`,                     // DashScope / Qwen
	`(?i)context[_ ]length[_ ]exceeded`,                       // generic
	`(?i)too many tokens`,                                     // generic
	`(?i)token limit exceeded`,                                // generic
	`(?i)maximum context length`,                              // OpenAI
	`(?i)prompt is too long`,                                  // generic
}, "|"))

// classifyStatusBody maps an HTTP status + response body to the unified
// taxonomy. Status codes decide first; message patterns refine the 4xx family
// (context-exceeded and content-policy surface as 400/413 on several vendors).
func classifyStatusBody(status int, body string) *ProviderError {
	snippet := body
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &ProviderError{Kind: ErrAuth, Status: status, Message: snippet}
	case status == http.StatusTooManyRequests:
		return &ProviderError{Kind: ErrRateLimited, Status: status, Message: snippet}
	case status >= 500:
		return &ProviderError{Kind: ErrProviderUpstream, Status: status, Message: snippet}
	}
	if contextOverflowPatterns.MatchString(body) {
		return &ProviderError{Kind: ErrContextExceeded, Status: status, Message: snippet}
	}
	if strings.Contains(strings.ToLower(body), "content_policy") ||
		strings.Contains(strings.ToLower(body), "content policy") {
		return &ProviderError{Kind: ErrContentPolicy, Status: status, Message: snippet}
	}
	return &ProviderError{Kind: ErrProviderUpstream, Status: status, Message: snippet}
}

// ClassifyError translates any provider-facing error into the unified
// taxonomy: ProviderError passes through, go-openai APIError classifies by
// its HTTP status, timeouts map to ErrTimeout, and everything else is
// provider_upstream at the network level (retryable — transient by nature).
func ClassifyError(err error) *ProviderError {
	if err == nil {
		return nil
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return classifyStatusBody(apiErr.HTTPStatusCode, apiErr.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		return &ProviderError{Kind: ErrTimeout, Message: err.Error(), Err: err}
	}
	if errors.Is(err, context.Canceled) {
		// Caller abort, not a provider failure. Marked with Status -1 so
		// IsRetryable's 5xx/network checks exclude it.
		return &ProviderError{Kind: ErrProviderUpstream, Status: -1, Message: err.Error(), Err: err}
	}
	return &ProviderError{Kind: ErrProviderUpstream, Status: 0, Message: err.Error(), Err: err}
}

func isNetTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// wrapInvokeError classifies an invoke failure and prefixes the action for
// log/trace context while preserving the original error for unwrapping.
func wrapInvokeError(action string, err error) error {
	pe := ClassifyError(err)
	pe.Message = action + ": " + pe.Message
	pe.Err = err
	return pe
}

// invokeRetryMax bounds provider-level retries (design §4.6: exponential
// backoff with jitter). 2 retries = 3 attempts total; asynq-level retries
// still provide the outer resilience for background tasks.
const invokeRetryMax = 2

// invokeRetryBaseDelay is the first backoff step, doubled per attempt with
// jitter. Small enough that an interactive caller barely notices one retry.
const invokeRetryBaseDelay = 500 * time.Millisecond

// withProviderRetry runs attempt, retrying while the failure classifies as
// retryable (rate limit / 5xx / network / timeout) and the context still
// lives. Non-retryable failures (auth, context-exceeded, content policy,
// other 4xx) and exhausted/dead contexts fail fast.
func withProviderRetry[T any](ctx context.Context, attempt func(context.Context) (T, error)) (T, error) {
	var zero T
	delay := invokeRetryBaseDelay
	for try := 0; ; try++ {
		result, err := attempt(ctx)
		if err == nil {
			return result, nil
		}
		if try >= invokeRetryMax || ctx.Err() != nil || !ClassifyError(err).IsRetryable() {
			return zero, err
		}
		jitter := time.Duration(rand.Int64N(int64(delay) / 2))
		select {
		case <-ctx.Done():
			return zero, err
		case <-time.After(delay + jitter):
		}
		delay *= 2
	}
}
