package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
)

type retryTestEmbedder struct {
	embedding.Embedder
	calls [][]string
	send  func(context.Context, []string) ([][]float32, error)
}

func (e *retryTestEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	e.calls = append(e.calls, append([]string(nil), texts...))
	return e.send(ctx, texts)
}

func fastEmbeddingRetries(t *testing.T) {
	t.Helper()
	t.Setenv("BATCH_EMBED_SIZE", "2")
	t.Setenv("EMBED_BATCH_INTERVAL_MS", "0")
	t.Setenv("EMBED_RETRY_ATTEMPTS", "3")
	t.Setenv("EMBED_RETRY_BASE_DELAY_MS", "1")
	t.Setenv("EMBED_RETRY_MAX_DELAY_MS", "2")
}

func TestEmbeddingDelayRejectsOverflow(t *testing.T) {
	for _, key := range []string{"EMBED_RETRY_BASE_DELAY_MS", "EMBED_RETRY_MAX_DELAY_MS", "EMBED_BATCH_INTERVAL_MS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, strconv.FormatInt(1<<63-1, 10))
			if got := embeddingDelay(key, time.Second); got != time.Second {
				t.Fatalf("overflowing milliseconds were accepted: %v", got)
			}
		})
	}
}

func TestEmbeddingRetryRetainsSuccessfulBatches(t *testing.T) {
	fastEmbeddingRetries(t)
	e := &retryTestEmbedder{}
	e.send = func(_ context.Context, texts []string) ([][]float32, error) {
		if len(e.calls) == 2 {
			return nil, fmt.Errorf("provider: %w", &api.HTTPError{StatusCode: 429})
		}
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			vectors[i] = []float32{float32(text[0])}
		}
		return vectors, nil
	}
	got, err := batchEmbedSequentially(context.Background(), e, []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{{"a", "b"}, {"c", "d"}, {"c", "d"}, {"e"}}
	if !reflect.DeepEqual(e.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", e.calls, wantCalls)
	}
	if !reflect.DeepEqual(got, [][]float32{{97}, {98}, {99}, {100}, {101}}) {
		t.Fatalf("vectors lost their input order: %v", got)
	}
}

func TestEmbeddingRetryHonorsRetryAfter(t *testing.T) {
	fastEmbeddingRetries(t)
	e := &retryTestEmbedder{}
	var failedAt time.Time
	e.send = func(context.Context, []string) ([][]float32, error) {
		if len(e.calls) == 1 {
			failedAt = time.Now()
			return nil, &api.HTTPError{StatusCode: 429, Header: http.Header{"Retry-After": {"1"}}}
		}
		if elapsed := time.Since(failedAt); elapsed < time.Second {
			t.Fatalf("retried before Retry-After: %v", elapsed)
		}
		return [][]float32{{1}}, nil
	}
	if _, err := batchEmbedWithBackoff(context.Background(), e, []string{"a"}); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddingRetryStopsOnPermanentErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			fastEmbeddingRetries(t)
			e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
				return nil, &api.HTTPError{StatusCode: status}
			}}
			_, err := batchEmbedWithBackoff(context.Background(), e, []string{"a"})
			if err == nil || len(e.calls) != 1 {
				t.Fatalf("permanent error was retried: calls=%d err=%v", len(e.calls), err)
			}
		})
	}
}

func TestEmbeddingRetryClassifiesWrappedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"rate limit", &api.HTTPError{StatusCode: 429}, true},
		{"server unavailable", &api.HTTPError{StatusCode: 503}, true},
		{"connection reset", &api.TransportError{Err: errors.New("connection reset")}, true},
		{"unauthorized", &api.HTTPError{StatusCode: 401}, false},
		{"invalid status", &api.HTTPError{StatusCode: 600}, false},
		{"cancelled", context.Canceled, false},
		{"expired context", &api.TransportError{Err: context.DeadlineExceeded}, false},
		{"invalid response", errors.New("invalid embedding response"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableEmbeddingError(fmt.Errorf("wrapped: %w", tc.err)); got != tc.want {
				t.Fatalf("retryable=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmbeddingRetryExhaustion(t *testing.T) {
	fastEmbeddingRetries(t)
	providerErr := &api.HTTPError{StatusCode: 429}
	e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
		return nil, providerErr
	}}
	_, err := batchEmbedSequentially(context.Background(), e, []string{"a"})
	if !errors.Is(err, providerErr) || len(e.calls) != 3 {
		t.Fatalf("calls=%d err=%v", len(e.calls), err)
	}
}

func TestBatchIndexDoesNotReplaySuccessAfterRetryExhaustion(t *testing.T) {
	fastEmbeddingRetries(t)
	t.Setenv("BATCH_EMBED_SIZE", "1")
	providerErr := &api.HTTPError{StatusCode: 429}
	e := &retryTestEmbedder{send: func(_ context.Context, texts []string) ([][]float32, error) {
		if texts[0] == "a" {
			return [][]float32{{1}}, nil
		}
		return nil, providerErr
	}}
	engine := &KeywordsVectorHybridRetrieveEngineService{}
	err := engine.BatchIndex(context.Background(), e, []*types.IndexInfo{
		{Content: "a"}, {Content: "b"}, {Content: "c"},
	}, []types.RetrieverType{types.VectorRetrieverType})
	if !errors.Is(err, providerErr) || !reflect.DeepEqual(e.calls, [][]string{{"a"}, {"b"}, {"b"}, {"b"}}) {
		t.Fatalf("successful batch replayed or later batch submitted: calls=%v err=%v", e.calls, err)
	}
}

func TestBatchIndexPrecomputedVectorsLargerThanEmbeddingBatch(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	repo := &recordingRepository{}
	engine := compositeOver(repo, types.VectorRetrieverType)
	model := &capturingEmbedder{}
	rows := []*types.IndexInfo{{SourceID: "a"}, {SourceID: "b"}, {SourceID: "c"}}
	err := engine.BatchIndexVectors(context.Background(), model, rows, [][]float32{{1}, {2}, {3}})
	if err != nil || !reflect.DeepEqual(repo.embeddings, map[string][]float32{"a": {1}, "b": {2}, "c": {3}}) {
		t.Fatalf("precomputed vectors misaligned: vectors=%v err=%v", repo.embeddings, err)
	}
	if len(model.batchTexts) != 0 {
		t.Fatal("precomputed vectors invoked the provider")
	}
}

func TestEmbeddingRetryAfterIsNotCapped(t *testing.T) {
	for _, header := range []string{"60", time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			fastEmbeddingRetries(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
				return nil, &api.HTTPError{StatusCode: 429, Header: http.Header{"Retry-After": {header}}}
			}}
			_, err := batchEmbedWithBackoff(ctx, e, []string{"a"})
			if !errors.Is(err, context.DeadlineExceeded) || len(e.calls) != 1 {
				t.Fatalf("provider minimum shortened to configured 2ms cap: calls=%d err=%v", len(e.calls), err)
			}
		})
	}
}

func TestEmbeddingRetryThroughCurrentProtocols(t *testing.T) {
	for _, provider := range []string{"generic", "volcengine"} {
		t.Run(provider, func(t *testing.T) {
			fastEmbeddingRetries(t)
			t.Setenv("BATCH_EMBED_SIZE", "1")
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":{"code":"AccountRateLimitExceeded","message":"rate limited"}}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if provider == "volcengine" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"embedding": []float32{1}}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": []any{map[string]any{"index": 0, "embedding": []float32{1}}},
					})
				}
			}))
			defer server.Close()
			pool, err := ants.NewPool(1)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Release()
			model := "text-embedding-3-small"
			if provider == "volcengine" {
				model = "doubao-embedding-vision-251215"
			}
			e, err := embedding.NewEmbedder(embedding.Config{
				Source: types.ModelSourceRemote, Provider: provider,
				BaseURL: server.URL, ModelName: model, ModelID: "retry-test",
			}, embedding.NewBatchEmbedder(pool), nil)
			if err != nil {
				t.Fatal(err)
			}
			vectors, err := batchEmbedSequentially(context.Background(), e, []string{"a"})
			if err != nil || hits.Load() != 2 || !reflect.DeepEqual(vectors, [][]float32{{1}}) {
				t.Fatalf("protocol retry failed: hits=%d vectors=%v err=%v", hits.Load(), vectors, err)
			}
		})
	}
}

func TestEmbeddingWaitsRespectCancellation(t *testing.T) {
	for _, rateLimited := range []bool{false, true} {
		t.Run(fmt.Sprint(rateLimited), func(t *testing.T) {
			fastEmbeddingRetries(t)
			t.Setenv("BATCH_EMBED_SIZE", "1")
			t.Setenv("EMBED_BATCH_INTERVAL_MS", "60000")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
				cancel()
				if rateLimited {
					return nil, &api.HTTPError{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}}
				}
				return [][]float32{{1}}, nil
			}}
			_, err := batchEmbedSequentially(ctx, e, []string{"a", "b"})
			if !errors.Is(err, context.Canceled) || len(e.calls) != 1 {
				t.Fatalf("calls=%d err=%v", len(e.calls), err)
			}
		})
	}
}

func TestEmbeddingBatchesArePaced(t *testing.T) {
	fastEmbeddingRetries(t)
	t.Setenv("BATCH_EMBED_SIZE", "1")
	t.Setenv("EMBED_BATCH_INTERVAL_MS", "20")
	var previous time.Time
	e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
		if !previous.IsZero() && time.Since(previous) < 20*time.Millisecond {
			t.Fatal("next batch sent before configured interval")
		}
		previous = time.Now()
		return [][]float32{{1}}, nil
	}}
	if _, err := batchEmbedSequentially(context.Background(), e, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddingRejectsWrongVectorCount(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fastEmbeddingRetries(t)
			e := &retryTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
				return make([][]float32, count), nil
			}}
			if _, err := batchEmbedSequentially(context.Background(), e, []string{"a"}); err == nil {
				t.Fatal("accepted mismatched vector count")
			}
			if len(e.calls) != 1 {
				t.Fatal("retried invalid provider output")
			}
		})
	}
}
