package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type processingStateFailureRepo struct {
	interfaces.KnowledgeRepository
	err   error
	calls int
}

func (r *processingStateFailureRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	r.calls++
	return r.err
}

func TestProcessingHandlersReturnProcessingStatePersistenceFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		task    func(*testing.T) *asynq.Task
		handle  func(*knowledgeService, context.Context, *asynq.Task) error
		message string
	}{
		{
			name: "document process",
			task: func(t *testing.T) *asynq.Task {
				t.Helper()
				payload, err := json.Marshal(types.DocumentProcessPayload{
					TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
				})
				require.NoError(t, err)
				return asynq.NewTask(types.TypeDocumentProcess, payload)
			},
			handle: func(s *knowledgeService, ctx context.Context, task *asynq.Task) error {
				return s.ProcessDocument(ctx, task)
			},
			message: "mark knowledge processing",
		},
		{
			name: "manual process",
			task: func(t *testing.T) *asynq.Task {
				t.Helper()
				payload, err := json.Marshal(types.ManualProcessPayload{
					TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "updated",
				})
				require.NoError(t, err)
				return asynq.NewTask(types.TypeManualProcess, payload)
			},
			handle: func(s *knowledgeService, ctx context.Context, task *asynq.Task) error {
				return s.ProcessManualUpdate(ctx, task)
			},
			message: "mark manual knowledge processing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDocumentWriteFixture(t)
			require.NoError(t, fixture.db.Model(&types.Knowledge{}).
				Where("id = ?", "doc").
				Update("parse_status", types.ParseStatusPending).Error)

			databaseErr := errors.New("database temporarily unavailable")
			failingRepo := &processingStateFailureRepo{
				KnowledgeRepository: fixture.repo,
				err:                 databaseErr,
			}
			fixture.svc.repo = failingRepo

			err := tc.handle(fixture.svc, context.Background(), tc.task(t))

			require.ErrorIs(t, err, databaseErr)
			require.ErrorContains(t, err, tc.message)
			require.Equal(t, 1, failingRepo.calls)
			require.Zero(t, fixture.chunkRepo.writes, "chunk mutations must not start")
			require.Zero(t, fixture.graph.calls, "graph cleanup must not start")
		})
	}
}
