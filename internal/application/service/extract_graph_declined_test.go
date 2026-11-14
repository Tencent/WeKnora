package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type graphReplyChat struct{ resp *types.ChatResponse }

func (c graphReplyChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return c.resp, nil
}

func (c graphReplyChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}
func (c graphReplyChat) GetModelName() string { return "graph-reply" }
func (c graphReplyChat) GetModelID() string   { return "graph-reply" }

type graphChunkRepo struct {
	interfaces.ChunkRepository
	chunk *types.Chunk
}

func (r graphChunkRepo) GetChunkByID(context.Context, uint64, string) (*types.Chunk, error) {
	return r.chunk, nil
}

type graphKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r graphKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

// Issue #3600: a prose refusal finishes the per-chunk task (no asynq retry),
// while the same prose cut off by the output budget stays a retriable error.
func TestChunkExtractHandle_ModelDeclinedIsSkipNotRetry(t *testing.T) {
	const prose = "抱歉，该文本为目录页，没有可抽取的实体和关系。"
	payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: "c1", ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		finish  string
		wantErr bool
	}{
		{"model stopped on its own: skip", "stop", false},
		{"output truncated: retry", "length", true},
		{"output truncated (max_tokens): retry", "max_tokens", true},
		{"stream broke before stop: retry", types.FinishReasonIncomplete, true},
		{"no finish reason reported: retry", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &ChunkExtractService{
				template: &types.PromptTemplateStructured{Description: "extract"},
				modelService: &stubModelService{chatModel: graphReplyChat{
					resp: &types.ChatResponse{Content: prose, FinishReason: tc.finish},
				}},
				knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{
					ID:            "kb1",
					ExtractConfig: &types.ExtractConfig{Enabled: true},
				}},
				chunkRepo: graphChunkRepo{chunk: &types.Chunk{
					ID: "c1", KnowledgeBaseID: "kb1", Content: "目录 …… 1",
				}},
			}
			err := svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Handle err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

type cancelingGraphChunkRepo struct {
	graphChunkRepo
	cancel context.CancelFunc
	err    error
}

func (r cancelingGraphChunkRepo) GetChunkByID(context.Context, uint64, string) (*types.Chunk, error) {
	r.cancel()
	return r.chunk, r.err
}

type graphOutcomeTracker struct {
	noopSpanTracker
	terminalContextErr error
	terminalTenant     any
	bounded            bool
	outcome            string
}

func (*graphOutcomeTracker) LookupStage(context.Context, string, int, string) *Span {
	return &Span{}
}

func (*graphOutcomeTracker) BeginSubSpan(context.Context, *Span, string, string, types.JSONMap) *Span {
	return &Span{}
}

func (t *graphOutcomeTracker) EndSpan(ctx context.Context, _ *Span, _ types.JSONMap) {
	t.recordOutcome(ctx, "success")
}

func (t *graphOutcomeTracker) FailSpan(ctx context.Context, _ *Span, code, _ string, _ error) {
	t.recordOutcome(ctx, code)
}

func (t *graphOutcomeTracker) recordOutcome(ctx context.Context, outcome string) {
	t.terminalContextErr = ctx.Err()
	t.terminalTenant = ctx.Value(types.TenantIDContextKey)
	_, t.bounded = ctx.Deadline()
	if t.terminalContextErr == nil {
		t.outcome = outcome
	}
}

type graphOutcomeKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	tracker          *graphOutcomeTracker
	outcomeAtRelease string
}

func (*graphOutcomeKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return nil, nil
}

func (r *graphOutcomeKnowledgeRepo) FinalizeSubtask(context.Context, string) (int, bool, error) {
	r.outcomeAtRelease = r.tracker.outcome
	return 0, true, nil
}

func TestChunkExtractHandle_RecordsTerminalOutcomeBeforeReleasingSlotWhenCanceled(t *testing.T) {
	payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: "c1", KnowledgeID: "k1", Attempt: 1})
	require.NoError(t, err)

	for _, tc := range []struct {
		name        string
		chunkErr    error
		wantOutcome string
	}{
		{name: "failure on final attempt", chunkErr: context.Canceled, wantOutcome: "GRAPH_EXTRACT_FAILED"},
		{name: "success after cancellation", wantOutcome: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(types.WithTaskRetryMetadata(context.Background(), 2, 2))
			defer cancel()
			tracker := &graphOutcomeTracker{}
			knowledges := &graphOutcomeKnowledgeRepo{tracker: tracker}
			svc := &ChunkExtractService{
				spanTracker:       tracker,
				knowledgeRepo:     knowledges,
				knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{ID: "kb1"}},
				chunkRepo: cancelingGraphChunkRepo{
					graphChunkRepo: graphChunkRepo{chunk: &types.Chunk{ID: "c1", KnowledgeBaseID: "kb1"}},
					cancel:         cancel,
					err:            tc.chunkErr,
				},
			}

			require.ErrorIs(t, svc.Handle(ctx, asynq.NewTask(types.TypeChunkExtract, payload)), tc.chunkErr)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.NoError(t, tracker.terminalContextErr)
			require.True(t, tracker.bounded)
			require.Equal(t, uint64(1), tracker.terminalTenant)
			require.Equal(t, tc.wantOutcome, knowledges.outcomeAtRelease)
		})
	}
}
