package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type retryFailedKnowledgeRepoStub struct {
	interfaces.KnowledgeRepository
	knowledgeByID map[string]*types.Knowledge
	errByID       map[string]error
}

func (s *retryFailedKnowledgeRepoStub) GetKnowledgeByID(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error) {
	if err := s.errByID[id]; err != nil {
		return nil, err
	}
	if knowledge := s.knowledgeByID[id]; knowledge != nil {
		return knowledge, nil
	}
	return nil, repository.ErrKnowledgeNotFound
}

func TestShouldSkipRetryFailedKnowledge(t *testing.T) {
	tests := []struct {
		name        string
		knowledge   *types.Knowledge
		loadErr     error
		wantSkip    bool
		wantMessage string
	}{
		{
			name:        "missing document is skipped",
			loadErr:     repository.ErrKnowledgeNotFound,
			wantSkip:    true,
			wantMessage: "Skipped document that no longer exists or is inaccessible",
		},
		{
			name: "document moved to another knowledge base is skipped",
			knowledge: &types.Knowledge{
				KnowledgeBaseID: "kb-other",
				ParseStatus:     types.ParseStatusFailed,
			},
			wantSkip:    true,
			wantMessage: "Skipped document that no longer belongs to this knowledge base",
		},
		{
			name: "document that is no longer failed is skipped",
			knowledge: &types.Knowledge{
				KnowledgeBaseID: "kb-target",
				ParseStatus:     types.ParseStatusPending,
			},
			wantSkip:    true,
			wantMessage: "Skipped document that is no longer failed",
		},
		{
			name: "failed document in target knowledge base is retried",
			knowledge: &types.Knowledge{
				KnowledgeBaseID: "kb-target",
				ParseStatus:     types.ParseStatusFailed,
			},
			wantSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkip, gotMessage := shouldSkipRetryFailedKnowledge(tt.knowledge, "kb-target", tt.loadErr)
			if gotSkip != tt.wantSkip {
				t.Fatalf("skip = %v, want %v", gotSkip, tt.wantSkip)
			}
			if gotMessage != tt.wantMessage {
				t.Fatalf("message = %q, want %q", gotMessage, tt.wantMessage)
			}
		})
	}
}

func TestProcessKnowledgeRetryFailedSkipsDocumentsThatNoLongerQualify(t *testing.T) {
	payload := types.KnowledgeRetryFailedPayload{
		TenantID:     1,
		TaskID:       "retry-task-1",
		KBID:         "kb-target",
		KnowledgeIDs: []string{"deleted", "moved", "pending"},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	svc := &knowledgeService{
		repo: &retryFailedKnowledgeRepoStub{
			knowledgeByID: map[string]*types.Knowledge{
				"moved": {
					ID:              "moved",
					KnowledgeBaseID: "kb-other",
					ParseStatus:     types.ParseStatusFailed,
				},
				"pending": {
					ID:              "pending",
					KnowledgeBaseID: "kb-target",
					ParseStatus:     types.ParseStatusPending,
				},
			},
			errByID: map[string]error{
				"deleted": repository.ErrKnowledgeNotFound,
			},
		},
	}

	if err := svc.ProcessKnowledgeRetryFailed(context.Background(), asynq.NewTask(types.TypeKnowledgeRetryFailed, payloadBytes)); err != nil {
		t.Fatalf("ProcessKnowledgeRetryFailed returned error: %v", err)
	}

	progress, err := svc.GetKnowledgeRetryFailedProgress(context.Background(), payload.TaskID)
	if err != nil {
		t.Fatalf("GetKnowledgeRetryFailedProgress returned error: %v", err)
	}
	if progress.Status != types.KBCloneStatusCompleted {
		t.Fatalf("Status = %q, want %q", progress.Status, types.KBCloneStatusCompleted)
	}
	if progress.Total != 3 || progress.Skipped != 3 || progress.Processed != 0 || progress.Failed != 0 {
		t.Fatalf("progress counts = total:%d processed:%d skipped:%d failed:%d, want total:3 processed:0 skipped:3 failed:0",
			progress.Total, progress.Processed, progress.Skipped, progress.Failed)
	}
	if progress.Progress != 100 {
		t.Fatalf("Progress = %d, want 100", progress.Progress)
	}
}

func TestProcessKnowledgeRetryFailedFailsOnTransientLoadError(t *testing.T) {
	payload := types.KnowledgeRetryFailedPayload{
		TenantID:     1,
		TaskID:       "retry-task-transient",
		KBID:         "kb-target",
		KnowledgeIDs: []string{"db-error"},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	svc := &knowledgeService{
		repo: &retryFailedKnowledgeRepoStub{
			errByID: map[string]error{
				"db-error": errors.New("database unavailable"),
			},
		},
	}

	if err := svc.ProcessKnowledgeRetryFailed(context.Background(), asynq.NewTask(types.TypeKnowledgeRetryFailed, payloadBytes)); err == nil {
		t.Fatal("ProcessKnowledgeRetryFailed returned nil, want transient load error")
	}

	progress, err := svc.GetKnowledgeRetryFailedProgress(context.Background(), payload.TaskID)
	if err != nil {
		t.Fatalf("GetKnowledgeRetryFailedProgress returned error: %v", err)
	}
	if progress.Status != types.KBCloneStatusFailed {
		t.Fatalf("Status = %q, want %q", progress.Status, types.KBCloneStatusFailed)
	}
	if progress.Error == "" {
		t.Fatal("Error is empty, want transient error detail")
	}
}
