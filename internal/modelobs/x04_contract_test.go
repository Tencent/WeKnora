package modelobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

func TestX04DetachedContextPreservesStrictTask(t *testing.T) {
	ctx := x04Context()
	detached := logger.CloneContext(ctx)
	require.True(t, policyFromContext(detached).strict)
	require.Equal(t, "strict:x04-task", SharingScope(detached))
	require.ErrorIs(
		t,
		types.RecordModelAccountingError(
			detached,
			errors.New(
				"fixture ledger failure",
			),
		),
		types.ErrModelAccounting,
	)
	require.ErrorIs(t, types.ModelAccountingError(ctx), types.ErrModelAccounting)
}

func TestX04PhysicalRerankBatches(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var actual atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual.Add(1)
		var body struct {
			Datas []any `json:"datas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		scores := make([]float64, len(body.Datas))
		if err := json.NewEncoder(
			w,
		).Encode(
			map[string]any{
				"code": 0,
				"data": map[string]any{
					"scores": scores,
				},
			},
		); err !=
			nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	inner, err := rerank.NewVolcengineReranker(
		&rerank.RerankerConfig{
			APIKey:    "fixture",
			AppSecret: "fixture",
			BaseURL:   server.URL,
			ModelName: "fixture",
		},
	)
	require.NoError(t, err)
	store := &recorderStore{}
	documents := make([]string, 110)
	for i := range documents {
		documents[i] = "fixture"
	}
	result, err := NewRecorder(
		store,
	).WrapReranker(
		&types.Model{
			ID:       "x04-model",
			TenantID: 7,
		},
		inner,
	).Rerank(
		x04Context(),
		"query",
		documents,
	)
	require.NoError(t, err)
	require.Len(t, result, 110)
	require.Equal(t, int64(3), actual.Load())
	require.Len(t, store.starts, 3)
	require.Len(t, store.completions, 3)
}

func TestX04ChatFallbackAttemptsHaveSeparateRecords(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var actual atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if actual.Add(1) == 1 {
			w.WriteHeader(400)
			if _, err := fmt.Fprint(w, "{\"error\":{\"message\":\"image not supported\","+
				"\"type\":\"invalid_request_error\"}}"); err != nil {
				t.Error(err)
			}
			return
		}
		if _, err := fmt.Fprint(w, "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},"+
			"\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,"+
			"\"completion_tokens\":0,\"total_tokens\":0}}"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	inner, err := chat.NewChat(
		&chat.ChatConfig{
			Source:    types.ModelSourceRemote,
			BaseURL:   server.URL,
			Provider:  "openai",
			ModelName: "fixture",
		},
		nil,
	)
	require.NoError(t, err)
	store := &recorderStore{}
	_, err = NewRecorder(
		store,
	).WrapChat(
		&types.Model{
			ID:       "x04-model",
			TenantID: 7,
		},
		inner,
	).Chat(
		x04Context(),
		[]chat.Message{
			{
				Role:    "user",
				Content: "fixture",
			},
		},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), actual.Load())
	require.Len(t, store.starts, 2)
	require.Len(t, store.completions, 2)
	require.Equal(t, types.ModelCallStatusError, store.completions[0].Status)
	require.Equal(t, types.ModelCallStatusSuccess, store.completions[1].Status)
}

func x04Context() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return WithEvaluationTask(WithPurpose(ctx, PurposeEvaluation, true), "x04-task")
}

func TestX04PhysicalEmbeddingBatchesAndRetries(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	t.Setenv("BATCH_EMBED_SIZE", "5")
	var actual atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := actual.Add(1)
		if n == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 2}}
		}
		if err := json.NewEncoder(
			w,
		).Encode(
			map[string]any{
				"data": data,
				"usage": map[string]int{
					"prompt_tokens": len(
						req.Input,
					),
					"total_tokens": len(
						req.Input,
					),
				},
			},
		); err !=
			nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	pool, err := ants.NewPool(3)
	require.NoError(t, err)
	defer pool.Release()
	inner, err := embedding.NewOpenAIEmbedder(
		"fixture",
		server.URL,
		"fixture",
		511,
		2,
		"x04-model",
		embedding.NewBatchEmbedder(
			pool,
		),
	)
	require.NoError(t, err)
	store := &recorderStore{}
	wrapped := NewRecorder(store).WrapEmbedder(&types.Model{ID: "x04-model", TenantID: 7}, inner)
	texts := make([]string, 12)
	for i := range texts {
		texts[i] = fmt.Sprintf("fixture %d", i)
	}
	output, err := wrapped.BatchEmbedWithPool(x04Context(), wrapped, texts)
	require.NoError(t, err)
	require.Len(t, output, 12)
	require.Equal(t, int64(4), actual.Load())
	require.Len(t, store.starts, 4)
	require.Len(t, store.completions, 4)
	errorsSeen := 0
	tokens := 0
	for _, c := range store.completions {
		if c.Status == types.ModelCallStatusError {
			errorsSeen++
		}
		if c.TotalTokens != nil {
			tokens += *c.TotalTokens
		}
	}
	require.Equal(t, 1, errorsSeen)
	require.Equal(t, 12, tokens)
	for _, r := range store.starts {
		require.Equal(t, "x04-task", r.EvaluationTaskID)
		require.Contains(t, string(r.ModelSnapshot), "attempt_sequence")
	}
}

func TestX04StrictEmbeddingLedgerFailureNeverRetries(t *testing.T) {
	for _, failure := range []string{"start", "terminal"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			var actual atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				actual.Add(1)
				if _, err := fmt.Fprint(
					w, "{\"data\":[{\"index\":0,\"embedding\":[1,2]}],\"usage\":{\"prompt_tokens\":0,"+
						"\"total_tokens\":0}}",
				); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			inner, err := embedding.NewOpenAIEmbedder("fixture", server.URL, "fixture", 511, 2, "x04-model", nil)
			require.NoError(t, err)
			store := &recorderStore{}
			if failure == "start" {
				store.startErr = errors.New("connection timeout in ledger")
			} else {
				store.completeErr = errors.New("connection timeout in ledger")
			}
			ctx := x04Context()
			_, err = NewRecorder(
				store,
			).WrapEmbedder(
				&types.Model{
					ID:       "x04-model",
					TenantID: 7,
				},
				inner,
			).BatchEmbed(
				ctx,
				[]string{
					"text",
				},
			)
			require.ErrorIs(t, err, types.ErrModelAccounting)
			require.ErrorIs(t, types.ModelAccountingError(ctx), types.ErrModelAccounting)
			if failure == "start" {
				require.Zero(t, actual.Load())
			} else {
				require.Equal(t, int64(1), actual.Load())
				require.Equal(t, 3, store.completeCalls)
			}
		})
	}
}

func TestX04StreamingSegmentsTailUsageAndSingleTerminal(t *testing.T) {
	stream := make(chan types.StreamResponse, 5)
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "reason"}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Done: true}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "answer"}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true}
	stream <- types.StreamResponse{Usage: &types.TokenUsage{UsageReported: true}}
	close(stream)
	store := &recorderStore{price: &types.ModelPriceVersion{Currency: "USD"}}
	out, err := NewRecorder(
		store,
	).WrapChat(
		&types.Model{
			ID:       "x04-model",
			TenantID: 7,
		},
		&recorderChat{
			stream: stream,
		},
	).ChatStream(
		x04Context(),
		nil,
		nil,
	)
	require.NoError(t, err)
	var responses []types.StreamResponse
	for r := range out {
		responses = append(responses, r)
	}
	require.Len(t, responses, 4)
	require.Equal(t, types.ResponseTypeThinking, responses[1].ResponseType)
	require.True(t, responses[1].Done)
	require.True(t, responses[3].Usage.UsageReported)
	require.NotNil(t, store.completions[0].TotalTokens)
	require.Zero(t, *store.completions[0].TotalTokens)
}

func TestX04CacheBillingDisjointBuckets(t *testing.T) {
	read, short, long := int64(200_000), int64(1_250_000), int64(2_000_000)
	write5, write1 := 10, 5
	u := &types.TokenUsage{
		UsageReported:      true,
		PromptTokens:       100,
		CompletionTokens:   10,
		CacheReported:      true,
		CacheReadTokens:    60,
		CacheWriteTokens:   15,
		CacheMissTokens:    40,
		CacheWrite5mTokens: &write5,
		CacheWrite1hTokens: &write1,
	}
	cost, err := CalculateUsageCostMicrounits(
		u,
		1_000_000,
		3_000_000,
		&types.ModelCachePricing{
			Version:                     1,
			ReadMicrounitsPerMillion:    &read,
			Write5mMicrounitsPerMillion: &short,
			Write1hMicrounitsPerMillion: &long,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(90), cost)
	_, err = CalculateUsageCostMicrounits(u, 1, 1, nil)
	require.Error(t, err)
}

func TestX04HTTPStreamRecordedAfterTailUsage(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},"+
			"\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],"+
			"\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,"+
			"\"total_tokens\":0}}\n\ndata: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	inner, err := chat.NewChat(
		&chat.ChatConfig{
			Source:    types.ModelSourceRemote,
			BaseURL:   server.URL,
			ModelName: "fixture",
			Provider:  "openai",
		},
		nil,
	)
	require.NoError(t, err)
	store := &recorderStore{}
	out, err := NewRecorder(
		store,
	).WrapChat(
		&types.Model{
			ID:       "x04-model",
			TenantID: 7,
		},
		inner,
	).ChatStream(
		x04Context(),
		nil,
		nil,
	)
	require.NoError(t, err)
	var terminal types.StreamResponse
	for r := range out {
		if r.Done {
			terminal = r
		}
	}
	require.Equal(t, types.ResponseTypeAnswer, terminal.ResponseType)
	require.Len(t, store.starts, 1)
	require.Len(t, store.completions, 1)
	require.Equal(t, types.ModelCallStatusSuccess, store.completions[0].Status)
	require.NotNil(t, store.completions[0].TotalTokens)
	require.Zero(t, *store.completions[0].TotalTokens)
}
