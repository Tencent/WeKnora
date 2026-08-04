package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestChunkFeedbackRecorderRecordsMessageChunkRefs(t *testing.T) {
	repo := &recordingQARefRepo{}
	recorder := &ChunkFeedbackRecorder{qaRefRepo: repo}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID: "session-1",
			TenantID:  7,
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb-owned", TenantID: 7},
				{KnowledgeBaseID: "kb-shared", TenantID: 99},
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "chunk-a", KnowledgeBaseID: "kb-owned", MatchType: types.MatchTypeEmbedding, SubChunkID: []string{"chunk-b", "chunk-c"}},
				{ID: "chunk-b", KnowledgeBaseID: "kb-owned", MatchType: types.MatchTypeKeywords},
				{ID: "shared-chunk", KnowledgeBaseID: "kb-shared", MatchType: types.MatchTypeEmbedding},
				{ID: "web-result", KnowledgeBaseID: "kb-owned", MatchType: types.MatchTypeWebSearch},
			},
		},
		PipelineContext: types.PipelineContext{
			MessageID: "message-1",
		},
	}

	err := recorder.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError { return nil })

	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	want := []struct {
		chunkID       string
		chunkTenantID uint64
	}{
		{"chunk-a", 7},
		{"chunk-b", 7},
		{"chunk-c", 7},
		{"shared-chunk", 99},
	}
	if len(repo.refs) != len(want) {
		t.Fatalf("saved refs = %#v, want %#v", repo.refs, want)
	}
	for i, ref := range repo.refs {
		if ref.MessageID != "message-1" || ref.TenantID != 7 || ref.ChunkID != want[i].chunkID || ref.ChunkTenantID != want[i].chunkTenantID {
			t.Fatalf("ref[%d] = %#v, want message=message-1 session-tenant=7 chunk=%s chunk-tenant=%d", i, ref, want[i].chunkID, want[i].chunkTenantID)
		}
	}
}

func TestChunkFeedbackRecorderUsesSessionTenantForSharedAgentRefs(t *testing.T) {
	repo := &recordingQARefRepo{}
	recorder := &ChunkFeedbackRecorder{qaRefRepo: repo}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID: "session-1",
			// Shared-agent retrieval runs under the source workspace.
			TenantID: 99,
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb-source", TenantID: 99},
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "chunk-source", KnowledgeBaseID: "kb-source", MatchType: types.MatchTypeEmbedding},
			},
		},
		PipelineContext: types.PipelineContext{MessageID: "message-1"},
	}
	ctx := context.WithValue(context.Background(), types.SessionTenantIDContextKey, uint64(7))

	err := recorder.OnEvent(ctx, types.INTO_CHAT_MESSAGE, cm, func() *PluginError { return nil })

	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if len(repo.refs) != 1 {
		t.Fatalf("saved refs = %#v, want one ref", repo.refs)
	}
	ref := repo.refs[0]
	if ref.TenantID != 7 || ref.ChunkTenantID != 99 {
		t.Fatalf("ref tenants = feedback:%d chunk:%d, want feedback:7 chunk:99", ref.TenantID, ref.ChunkTenantID)
	}
}

type recordingQARefRepo struct {
	refs []*types.QAReplyChunkRef
}

func (r *recordingQARefRepo) Create(ctx context.Context, ref *types.QAReplyChunkRef) error {
	r.refs = append(r.refs, ref)
	return nil
}

func (r *recordingQARefRepo) CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error {
	r.refs = append(r.refs, refs...)
	return nil
}

func (r *recordingQARefRepo) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error) {
	return nil, nil
}

func (r *recordingQARefRepo) GetByChunkID(ctx context.Context, tenantID uint64, chunkID string) ([]*types.QAReplyChunkRef, error) {
	return nil, nil
}

func (r *recordingQARefRepo) CreateResetTombstones(ctx context.Context, refs []*types.QAReplyChunkRef, operator string) error {
	return nil
}

func (r *recordingQARefRepo) GetResetTombstonesByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRefTombstone, error) {
	return nil, nil
}

func (r *recordingQARefRepo) DeleteByMessageID(ctx context.Context, tenantID uint64, messageID string) error {
	return nil
}

func (r *recordingQARefRepo) DeleteByChunkID(ctx context.Context, chunkTenantID uint64, chunkID string) error {
	return nil
}

func (r *recordingQARefRepo) CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	return 0, nil
}

func (r *recordingQARefRepo) CountSessionsByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	return 0, nil
}
