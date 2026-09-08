package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/usage"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
)

type fakeUsageRepo struct {
	mu     sync.Mutex
	usages []*types.ModelUsage
	err    error
}

func (f *fakeUsageRepo) Create(_ context.Context, u *types.ModelUsage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.usages = append(f.usages, u)
	return nil
}

func (f *fakeUsageRepo) last() *types.ModelUsage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.usages) == 0 {
		return nil
	}
	return f.usages[len(f.usages)-1]
}

func (f *fakeUsageRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.usages)
}

type usageFakeChat struct {
	resp     *types.ChatResponse
	stream   []types.StreamResponse
	err      error
	requests int // outbound requests to simulate (default 1)
}

func (f *usageFakeChat) noteRequests(ctx context.Context) {
	n := f.requests
	if n <= 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		noteChatProviderRequest(ctx)
	}
}

func (f *usageFakeChat) Chat(ctx context.Context, _ []Message, _ *ChatOptions) (*types.ChatResponse, error) {
	f.noteRequests(ctx)
	return f.resp, f.err
}

func (f *usageFakeChat) ChatStream(
	ctx context.Context, _ []Message, _ *ChatOptions,
) (<-chan types.StreamResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.noteRequests(ctx)
	ch := make(chan types.StreamResponse)
	go func() {
		defer close(ch)
		for _, r := range f.stream {
			select {
			case ch <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (f *usageFakeChat) GetModelName() string { return "fake" }
func (f *usageFakeChat) GetModelID() string   { return "fake-id" }

func testChatConfig() ChatConfig {
	return ChatConfig{
		ModelID: "chat-model", ModelName: "gpt-safe", Type: types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceOpenAI, Provider: "openai", TenantID: 10000,
	}
}

func tenantCtx(id uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, id)
}

func TestChatUsageNonStreamProviderReported(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	inner := &usageFakeChat{resp: &types.ChatResponse{
		Usage: types.TokenUsage{
			PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
			TokenProvenance: types.TokenProvenanceProviderReported,
		},
	}}
	resolved := "provider-model"
	w, err := wrapChatUsage(inner, ptr(testChatConfig()), &resolved, nil)
	require.NoError(t, err)

	before := time.Now()
	resp, err := w.Chat(tenantCtx(1), nil, nil)
	after := time.Now()
	require.NoError(t, err)
	require.NotNil(t, resp)

	u := repo.last()
	require.NotNil(t, u)
	require.Equal(t, types.CallTypeChat, u.CallType)
	require.Equal(t, uint64(1), u.TenantID)
	require.Equal(t, uint64(10000), u.ModelTenantID)
	require.Equal(t, "openai", u.ResolvedProvider)
	require.Equal(t, "provider-model", *u.ResolvedModelName)
	require.NotNil(t, u.StartedAt)
	require.False(t, u.StartedAt.Before(before))
	require.False(t, u.StartedAt.After(after))
	require.Equal(t, 100, *u.InputTokens)
	require.Equal(t, 50, *u.OutputTokens)
	require.Equal(t, 150, *u.TotalTokens)
	require.Equal(t, types.TokenProvenanceProviderReported, u.TokenProvenance)
	require.Equal(t, types.UsageStatusSuccess, u.Status)
	require.Equal(t, 1, u.LogicalRequests)
	require.Equal(t, 1, u.ProviderRequests)
	require.Nil(t, u.EvaluationRunID, "ordinary chat must have no evaluation run id")
}

func TestEffectiveChatModelNameNormalAndRemoteOverride(t *testing.T) {
	normal, err := NewRemoteAPIChat(&ChatConfig{ModelName: "configured", Provider: "openai"})
	require.NoError(t, err)
	require.Equal(t, "configured", *effectiveChatModelName(normal))

	override, err := NewRemoteAPIChat(&ChatConfig{
		ModelName: "configured", Provider: "openai",
		ExtraConfig: map[string]string{"remote_model_name": "provider-effective"},
	})
	require.NoError(t, err)
	require.Equal(t, "provider-effective", *effectiveChatModelName(override))
}

func TestChatUsageStartedAtUsesRecordStart(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)
	w, _ := wrapChatUsage(&usageFakeChat{}, ptr(testChatConfig()), nil, nil)
	start := time.Date(2026, 8, 31, 1, 2, 3, 4, time.UTC)
	w.(*usageChat).record(tenantCtx(1), &chatUsageSpan{}, start, nil, nil, nil)
	require.Equal(t, start, *repo.last().StartedAt)
}

func TestChatUsageStreamSingleRow(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	usage1 := types.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		TokenProvenance: types.TokenProvenanceProviderReported,
	}
	inner := &usageFakeChat{stream: []types.StreamResponse{
		{Content: "hello"},
		{Content: " world", Done: true, Usage: &usage1},
	}}
	w, err := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	require.NoError(t, err)

	ch, err := w.ChatStream(tenantCtx(1), nil, nil)
	require.NoError(t, err)
	var got []types.StreamResponse
	for r := range ch {
		got = append(got, r)
	}
	require.Len(t, got, 2)

	u := repo.last()
	require.NotNil(t, u)
	require.Equal(t, 15, *u.TotalTokens)
	require.Equal(t, types.UsageStatusSuccess, u.Status)
	require.Equal(t, 1, repo.count()) // one row for the whole stream
}

func TestChatUsagePromptCacheStatus(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usage       types.TokenUsage
		wantStatus  types.PromptCacheStatus
		wantRead    bool
		wantReadVal int
	}{
		{"hit", tokenUsageWithCache(types.PromptCacheStatusHit, 40, 0, 60, true), types.PromptCacheStatusHit, true, 40},
		{"unsupported", func() types.TokenUsage {
			u := types.TokenUsage{PromptTokens: 100, TotalTokens: 100}
			u.MarkPromptCacheUnsupported()
			return u
		}(), types.PromptCacheStatusUnsupported, false, 0},
		{"unreported", func() types.TokenUsage {
			u := types.TokenUsage{PromptTokens: 100, TotalTokens: 100}
			u.SetPromptCacheUsage(0, 0, 0, false)
			return u
		}(), types.PromptCacheStatusUnreported, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeUsageRepo{}
			usage.SetRecorder(usage.NewRecorder(repo))
			defer usage.SetRecorder(nil)

			inner := &usageFakeChat{resp: &types.ChatResponse{Usage: tc.usage}}
			w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
			_, err := w.Chat(tenantCtx(1), nil, nil)
			require.NoError(t, err)

			u := repo.last()
			require.NotNil(t, u.PromptCacheStatus)
			require.Equal(t, tc.wantStatus, *u.PromptCacheStatus)
			if tc.wantRead {
				require.NotNil(t, u.CacheReadTokens)
				require.Equal(t, tc.wantReadVal, *u.CacheReadTokens)
			} else {
				require.Nil(t, u.CacheReadTokens, "unreported/unsupported cache tokens must be NULL, not 0")
			}
		})
	}
}

func TestChatUsageCancelledAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status types.UsageStatus
	}{
		{"cancelled", context.Canceled, types.UsageStatusCancelled},
		{"timeout", context.DeadlineExceeded, types.UsageStatusTimeout},
		{"error", errors.New("provider 500"), types.UsageStatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeUsageRepo{}
			usage.SetRecorder(usage.NewRecorder(repo))
			defer usage.SetRecorder(nil)

			inner := &usageFakeChat{err: tc.err}
			w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
			_, _ = w.Chat(tenantCtx(1), nil, nil)

			u := repo.last()
			require.NotNil(t, u)
			require.Equal(t, tc.status, u.Status)
		})
	}
}

func TestChatUsagePersistenceFailureDoesNotBreakCall(t *testing.T) {
	repo := &fakeUsageRepo{err: errors.New("db down")}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	inner := &usageFakeChat{resp: &types.ChatResponse{Usage: types.TokenUsage{PromptTokens: 10, TotalTokens: 10}}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	resp, err := w.Chat(tenantCtx(1), nil, nil)
	require.NoError(t, err, "model call must succeed even when usage persistence fails")
	require.NotNil(t, resp)
}

func TestChatUsageEvaluationAttribution(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	inner := &usageFakeChat{resp: &types.ChatResponse{Usage: types.TokenUsage{PromptTokens: 10, TotalTokens: 10}}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)

	ctx := types.WithEvaluationRunID(tenantCtx(1), "run-42")
	_, err := w.Chat(ctx, nil, nil)
	require.NoError(t, err)

	u := repo.last()
	require.NotNil(t, u.EvaluationRunID)
	require.Equal(t, "run-42", *u.EvaluationRunID)
}

func tokenUsageWithCache(status types.PromptCacheStatus, read, write, miss int, reported bool) types.TokenUsage {
	u := types.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	u.SetPromptCacheUsage(read, write, miss, reported)
	u.CacheStatus = status
	return u
}

func TestChatUsageTokenProvenance(t *testing.T) {
	cases := []struct {
		name   string
		usage  types.TokenUsage
		want   types.TokenProvenance
		inTok  *int
		outTok *int
		totTok *int
	}{
		{
			"provider_reported",
			types.TokenUsage{
				PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
				TokenProvenance: types.TokenProvenanceProviderReported,
			},
			types.TokenProvenanceProviderReported, intPtr(10), intPtr(5), intPtr(15),
		},
		{
			"derived",
			types.TokenUsage{
				PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
				TokenProvenance: types.TokenProvenanceDerived,
			},
			types.TokenProvenanceDerived, intPtr(10), intPtr(5), intPtr(15),
		},
		{
			"unreported",
			types.TokenUsage{},
			types.TokenProvenanceUnreported, nil, nil, nil,
		},
		{
			"unsupported",
			types.TokenUsage{TokenProvenance: types.TokenProvenanceUnsupported},
			types.TokenProvenanceUnsupported, nil, nil, nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mu := &types.ModelUsage{}
			applyChatUsage(mu, &tc.usage)
			require.Equal(t, tc.want, mu.TokenProvenance)
			if tc.inTok != nil {
				require.Equal(t, *tc.inTok, *mu.InputTokens)
			} else {
				require.Nil(t, mu.InputTokens)
			}
			if tc.totTok != nil {
				require.Equal(t, *tc.totTok, *mu.TotalTokens)
			} else {
				require.Nil(t, mu.TotalTokens)
			}
		})
	}
}

func TestChatUsageProviderRequestsCounter(t *testing.T) {
	ctx, span := withChatUsageSpan(context.Background())
	noteChatProviderRequest(ctx)
	noteChatProviderRequest(ctx)
	require.Equal(t, int64(2), span.providerRequests.Load(), "fallback/retry attempts must accumulate")
}

func TestChatUsageProviderRequestsFallback(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	inner := &usageFakeChat{
		requests: 2, // a multimodal fallback issues a second request
		resp: &types.ChatResponse{Usage: types.TokenUsage{
			PromptTokens: 10, TotalTokens: 10, TokenProvenance: types.TokenProvenanceProviderReported,
		}},
	}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	_, err := w.Chat(tenantCtx(1), nil, nil)
	require.NoError(t, err)

	u := repo.last()
	require.Equal(t, 2, u.ProviderRequests)
	require.Equal(t, 1, u.LogicalRequests)
}

func TestChatUsagePromptCacheHitWithZeroBaseTokens(t *testing.T) {
	// A full prompt-cache hit can report cache reads with zero base tokens; the
	// provenance must still be the reported cache status, not "unreported".
	u := types.TokenUsage{}
	u.SetPromptCacheUsage(50, 0, 0, true)
	u.CacheStatus = types.PromptCacheStatusHit

	mu := &types.ModelUsage{}
	applyChatUsage(mu, &u)
	require.NotNil(t, mu.PromptCacheStatus)
	require.Equal(t, types.PromptCacheStatusHit, *mu.PromptCacheStatus)
	require.NotNil(t, mu.CacheReadTokens)
	require.Equal(t, 50, *mu.CacheReadTokens)
}

func intPtr(v int) *int { return &v }

func ptr[T any](v T) *T { return &v }

func TestTokenUsageFromOpenAIPresence(t *testing.T) {
	// A reported all-zero usage block is provider_reported with 0, not unreported.
	u := tokenUsageFromOpenAI(openai.Usage{}, provider.ProviderOpenAI, true)
	require.Equal(t, types.TokenProvenanceProviderReported, u.TokenProvenance)

	// An omitted usage block leaves provenance empty (→ unreported downstream).
	u = tokenUsageFromOpenAI(openai.Usage{}, provider.ProviderOpenAI, false)
	require.Equal(t, types.TokenProvenance(""), u.TokenProvenance)
}

func TestChatUsageTokenProvenanceReportedZero(t *testing.T) {
	u := types.TokenUsage{
		PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0,
		TokenProvenance: types.TokenProvenanceProviderReported,
	}
	mu := &types.ModelUsage{}
	applyChatUsage(mu, &u)
	require.Equal(t, types.TokenProvenanceProviderReported, mu.TokenProvenance)
	require.NotNil(t, mu.InputTokens)
	require.Equal(t, 0, *mu.InputTokens)
	require.NotNil(t, mu.TotalTokens)
	require.Equal(t, 0, *mu.TotalTokens)
}

// streamFuncChat builds its stream channel from a per-invocation function.
type streamFuncChat struct {
	fn func(context.Context) <-chan types.StreamResponse
}

func (c *streamFuncChat) Chat(context.Context, []Message, *ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (c *streamFuncChat) ChatStream(
	ctx context.Context, _ []Message, _ *ChatOptions,
) (<-chan types.StreamResponse, error) {
	return c.fn(ctx), nil
}
func (c *streamFuncChat) GetModelName() string { return "fake" }
func (c *streamFuncChat) GetModelID() string   { return "fake-id" }

func TestChatUsageStreamProviderError(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	partial := types.TokenUsage{
		PromptTokens:     11,
		CompletionTokens: 7,
		TotalTokens:      18,
		TokenProvenance:  types.TokenProvenanceProviderReported,
	}
	inner := &usageFakeChat{stream: []types.StreamResponse{{
		ResponseType: types.ResponseTypeError,
		Done:         true,
		Usage:        &partial,
	}}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	ch, err := w.ChatStream(tenantCtx(1), nil, nil)
	require.NoError(t, err)
	for range ch {
		continue
	}

	require.Equal(t, 1, repo.count())
	got := repo.last()
	require.Equal(t, types.UsageStatusError, got.Status)
	require.Equal(t, 1, got.ProviderRequests)
	require.Equal(t, 11, *got.InputTokens)
	require.Equal(t, 7, *got.OutputTokens)
	require.Equal(t, 18, *got.TotalTokens)
	require.Equal(t, types.TokenProvenanceProviderReported, got.TokenProvenance)
}

func TestChatUsageStreamCancelled(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	ctx, cancel := context.WithCancel(tenantCtx(1))
	inner := &streamFuncChat{fn: func(sctx context.Context) <-chan types.StreamResponse {
		ch := make(chan types.StreamResponse)
		go func() {
			defer close(ch)
			select {
			case ch <- types.StreamResponse{Content: "first"}:
			case <-sctx.Done():
				return
			}
			<-sctx.Done()
		}()
		return ch
	}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	out, err := w.ChatStream(ctx, nil, nil)
	require.NoError(t, err)

	require.Equal(t, "first", (<-out).Content)
	cancel()
	drainStream(t, out)

	require.Equal(t, 1, repo.count())
	require.Equal(t, types.UsageStatusCancelled, repo.last().Status)
}

func TestChatUsageStreamTimeout(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	ctx, cancel := context.WithTimeout(tenantCtx(1), 50*time.Millisecond)
	defer cancel()
	inner := &streamFuncChat{fn: func(sctx context.Context) <-chan types.StreamResponse {
		ch := make(chan types.StreamResponse)
		go func() {
			defer close(ch)
			<-sctx.Done()
		}()
		return ch
	}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	out, err := w.ChatStream(ctx, nil, nil)
	require.NoError(t, err)

	drainStream(t, out) // deadline fires, stream ends

	require.Equal(t, 1, repo.count())
	require.Equal(t, types.UsageStatusTimeout, repo.last().Status)
}

func TestChatUsageStreamDownstreamStop(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	ctx, cancel := context.WithCancel(tenantCtx(1))
	defer cancel()
	inner := &streamFuncChat{fn: func(sctx context.Context) <-chan types.StreamResponse {
		ch := make(chan types.StreamResponse)
		go func() {
			defer close(ch)
			for _, r := range []types.StreamResponse{{Content: "first"}, {Content: "second"}} {
				select {
				case ch <- r:
				case <-sctx.Done():
					return
				}
			}
		}()
		return ch
	}}
	w, _ := wrapChatUsage(inner, ptr(testChatConfig()), nil, nil)
	out, err := w.ChatStream(ctx, nil, nil)
	require.NoError(t, err)

	require.Equal(t, "first", (<-out).Content)
	// Stop consuming out, then cancel; the wrapper goroutine must unblock on
	// ctx.Done and terminal-record exactly once rather than leak.
	cancel()
	waitForRows(t, repo, 1)
	require.Equal(t, types.UsageStatusCancelled, repo.last().Status)
}

func drainStream(t *testing.T, ch <-chan types.StreamResponse) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream did not close within timeout")
		}
	}
}

func waitForRows(t *testing.T, repo *fakeUsageRepo, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if repo.count() == n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected %d usage rows, got %d", n, repo.count())
		case <-ticker.C:
		}
	}
}
