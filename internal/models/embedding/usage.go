package embedding

import (
	"context"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// TokenUsage is the embedding-specific usage shape. Embeddings have input
// tokens only, but TotalTokens is kept because several providers expose that
// field directly.
type TokenUsage struct {
	InputTokens int
	TotalTokens int
	Source      string
}

type usageCaptureKey struct{}

// UsageCapture accumulates provider usage for one logical embedding call. A
// batch implementation may issue several provider requests, so values add.
// parent lets nested decorators (quota + Langfuse) observe the same reports.
type UsageCapture struct {
	mu     sync.Mutex
	usage  TokenUsage
	parent *UsageCapture
}

// WithUsageCapture installs a request-scoped usage collector.
func WithUsageCapture(ctx context.Context) (context.Context, *UsageCapture) {
	parent, _ := ctx.Value(usageCaptureKey{}).(*UsageCapture)
	capture := &UsageCapture{parent: parent}
	return context.WithValue(ctx, usageCaptureKey{}, capture), capture
}

// Usage returns the accumulated usage snapshot.
func (c *UsageCapture) Usage() TokenUsage {
	if c == nil {
		return TokenUsage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func reportTokenUsage(ctx context.Context, input, total int, source string) {
	if total <= 0 {
		total = input
	}
	if input <= 0 {
		input = total
	}
	if input <= 0 && total <= 0 {
		return
	}
	for capture, _ := ctx.Value(usageCaptureKey{}).(*UsageCapture); capture != nil; capture = capture.parent {
		capture.mu.Lock()
		capture.usage.InputTokens += input
		capture.usage.TotalTokens += total
		if capture.usage.Source == "" {
			capture.usage.Source = source
		} else if capture.usage.Source != source {
			capture.usage.Source = "provider:mixed"
		}
		capture.mu.Unlock()
	}
}

// estimateEmbeddingUsage is used only when the provider omits usage. It uses
// the configured model tokenizer where known, and cl100k_base otherwise. This
// replaces the old rune_count/4 rule that undercounted CJK input heavily.
func estimateEmbeddingUsage(texts []string, modelName string) TokenUsage {
	codec, err := tokenizer.ForModel(tokenizer.Model(strings.ToLower(strings.TrimSpace(modelName))))
	source := "tokenizer:model"
	if err != nil {
		codec, err = tokenizer.Get(tokenizer.Cl100kBase)
		source = "tokenizer:fallback:cl100k_base"
	}
	if err != nil {
		return TokenUsage{}
	}
	total := 0
	for _, text := range texts {
		if text == "" {
			continue
		}
		ids, _, encodeErr := codec.Encode(text)
		if encodeErr != nil {
			total += (len([]rune(text)) + 2) / 3
			continue
		}
		total += len(ids)
	}
	return TokenUsage{InputTokens: total, TotalTokens: total, Source: source}
}
