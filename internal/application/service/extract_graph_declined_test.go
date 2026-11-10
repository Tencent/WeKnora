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
