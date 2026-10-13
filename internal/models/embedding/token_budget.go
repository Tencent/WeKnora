package embedding

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
)

// ErrBudgetRetriesExhausted tells callers not to repeat the whole document
// after the provider wrapper has exhausted quota retries for one sub-batch.
var ErrBudgetRetriesExhausted = errors.New("embedding quota retries exhausted")

// Budget is shared by provider endpoint and model, including duplicate model
// records and requests from different documents within this process only. It paces estimated tokens
// rather than letting every document spend an independent minute allowance.
type tokenBudget struct {
	slot      chan struct{}
	next      time.Time
	tpm       int
	window    time.Duration
	completed int
	tokens    int
}

var embeddingBudgets = struct {
	sync.Mutex
	values map[string]*tokenBudget
}{values: make(map[string]*tokenBudget)}

func embeddingTokenBudget(c Config) *tokenBudget {
	if !strings.EqualFold(string(c.Source), "remote") {
		return nil
	}
	// Opt in to a provider budget; an unset limit preserves existing throughput.
	tpm, err := strconv.Atoi(os.Getenv("WEKNORA_EMBEDDING_TPM"))
	if err != nil || tpm <= 0 {
		return nil
	}
	key := strings.TrimRight(c.BaseURL, "/") + "\x00" + c.ModelName
	embeddingBudgets.Lock()
	defer embeddingBudgets.Unlock()
	if b := embeddingBudgets.values[key]; b != nil {
		return b
	}
	b := &tokenBudget{slot: make(chan struct{}, 1), tpm: tpm, window: time.Minute}
	embeddingBudgets.values[key] = b
	return b
}

func estimatedEmbeddingTokens(texts []string) int {
	n := 16
	for _, text := range texts {
		n += 2*utf8.RuneCountInString(text) + 8
	}
	return n
}

func waitEmbeddingBudget(ctx context.Context, until time.Time) error {
	if d := time.Until(until); d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// IsRateLimited identifies a provider quota rejection for shared cooldown and retry.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "insufficient_quota") || strings.Contains(s, "throttling")
}

func (b *tokenBudget) call(ctx context.Context, texts []string, call func() ([][]float32, error)) ([][]float32, error) {
	select {
	case b.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-b.slot }()
	for attempt := 0; ; attempt++ {
		if err := waitEmbeddingBudget(ctx, b.next); err != nil {
			return nil, err
		}
		spacing := time.Duration(float64(b.window) * float64(estimatedEmbeddingTokens(texts)) / float64(b.tpm))
		b.next = time.Now().Add(spacing)
		result, err := call()
		if !IsRateLimited(err) {
			if err == nil {
				b.completed++
				b.tokens += estimatedEmbeddingTokens(texts)
				if b.completed == 1 || b.completed%100 == 0 {
					logger.Infof(ctx, "Embedding shared budget progress: batches=%d estimated_tokens=%d budget_tpm=%d",
						b.completed, b.tokens, b.tpm)
				}
			}
			return result, err
		}
		// Other documents share this cooldown. Retrying the small failed request
		// preserves successful sub-batches instead of re-embedding the whole book.
		if b.tpm > 5000 {
			b.tpm /= 2
			if b.tpm < 5000 {
				b.tpm = 5000
			}
		}
		b.next = time.Now().Add(b.window)
		logger.Warnf(ctx, "Embedding quota hit: shared cooldown=%s budget_tpm=%d attempt=%d/5",
			b.window, b.tpm, attempt+1)
		if attempt >= 4 {
			return result, errors.Join(ErrBudgetRetriesExhausted, err)
		}
	}
}
