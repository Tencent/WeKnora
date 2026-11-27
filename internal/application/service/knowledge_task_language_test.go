package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func publishManualDoc(t *testing.T, f *documentWriteFixture) {
	t.Helper()
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		row.SetManualMetadata(types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1)),
	)
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
}

func enqueuedLanguage(t *testing.T, q *autoTagRecordingQueue) string {
	t.Helper()
	require.NotNil(t, q.task)
	var payload struct {
		Language string `json:"language"`
	}
	require.NoError(t, json.Unmarshal(q.task.Payload(), &payload))
	return payload.Language
}

func TestReparseTaskPreservesLanguage(t *testing.T) {
	for _, language := range []string{"ru-RU", ""} {
		t.Run("language="+language, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			publishManualDoc(t, f)
			q := &autoTagRecordingQueue{}
			f.svc.task = q
			raw, err := json.Marshal(types.KnowledgeListReparsePayload{
				TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc"}, Language: language,
			})
			require.NoError(t, err)
			require.NoError(t, f.svc.ProcessKnowledgeListReparse(
				context.Background(), asynq.NewTask(types.TypeKnowledgeListReparse, raw),
			))
			require.Equal(t, types.TypeManualProcess, q.task.Type())
			want := language
			if want == "" {
				want = types.DefaultLanguage()
			}
			require.Equal(t, want, enqueuedLanguage(t, q))
		})
	}
}

func TestMoveReparsePreservesLanguage(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	publishManualDoc(t, f)
	q := &autoTagRecordingQueue{}
	f.svc.task = q
	raw, err := json.Marshal(types.KnowledgeMovePayload{
		TenantID: 7, TaskID: "transfer-task", SourceKBID: "kb", TargetKBID: "other",
		KnowledgeIDs: []string{"doc"}, Mode: "reparse", Language: "ru-RU",
	})
	require.NoError(t, err)
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), asynq.NewTask(types.TypeKnowledgeMove, raw)))
	require.Equal(t, types.TypeManualProcess, q.task.Type())
	require.Equal(t, "ru-RU", enqueuedLanguage(t, q))
}

func TestDeleteTaskPreservesWikiRetractLanguage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		language string
	}{
		{name: "request language", language: "ru-RU"},
		{name: "legacy payload"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			f.kbs.values["kb"].IndexingStrategy.WikiEnabled = true
			f.svc.wikiRepo = &moveWikiPageRepo{}
			f.svc.wikiService = &moveWikiMetaService{}
			pending := &moveWikiPendingRepo{}
			f.svc.taskPendingRepo = pending
			f.svc.task = &autoTagRecordingQueue{}
			payload := map[string]interface{}{
				"tenant_id": 7, "knowledge_base_id": "kb", "knowledge_ids": []string{"doc"},
			}
			if tt.language != "" {
				payload["language"] = tt.language
			}
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			ctx := context.Background()
			require.NoError(t, f.svc.ProcessKnowledgeListDelete(ctx, asynq.NewTask(types.TypeKnowledgeListDelete, raw)))
			require.NotEmpty(t, pending.ops)
			var op WikiPendingOp
			require.NoError(t, json.Unmarshal(pending.ops[0].Payload, &op))
			require.Equal(t, WikiOpRetract, op.Op)
			want := tt.language
			if want == "" {
				want = types.DefaultLanguage()
			}
			require.Equal(t, want, op.Language)
		})
	}
}
