package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReparseSubmissionHashIsOrderIndependentAndConfigSensitive(t *testing.T) {
	first, err := reparseSubmissionHash("kb", "batch", []string{"b", "a"}, &types.KnowledgeProcessOverrides{})
	require.NoError(t, err)
	second, err := reparseSubmissionHash("kb", "batch", []string{"a", "b"}, &types.KnowledgeProcessOverrides{})
	require.NoError(t, err)
	require.Equal(t, first, second, "reordered selections are the same action")

	changed, err := reparseSubmissionHash("kb", "batch", []string{"a", "b"}, nil)
	require.NoError(t, err)
	require.NotEqual(t, first, changed, "an idempotency key must reject changed process configuration")

	otherBatch, err := reparseSubmissionHash(
		"kb",
		"other-batch",
		[]string{"a", "b"},
		&types.KnowledgeProcessOverrides{},
	)
	require.NoError(t, err)
	require.NotEqual(t, first, otherBatch, "an idempotency key must not move an action to another batch")
}

func TestFrozenReparseMembersPermitSubsetButRejectOutsideSelection(t *testing.T) {
	members := []*types.ReparseBatchMember{{KnowledgeID: "b"}, {KnowledgeID: "a"}}
	require.True(t, frozenMembersContain(members, []string{"a", "b"}))
	require.True(t, frozenMembersContain(members, []string{"a"}))
	require.False(t, frozenMembersContain(members, []string{"a", "b", "c"}))
}

func TestPrepareReparseSubmissionReplaysSameKeyAndRejectsChangedBody(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.ReparseBatch{}, &types.ReparseBatchMember{},
		&types.ReparseBatchSubmission{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX reparse_submission_test_key ON reparse_batch_submissions "+
			"(tenant_id, idempotency_key) WHERE idempotency_key <> ''",
	).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "a", TenantID: 7, KnowledgeBaseID: "kb", Type: "file", Title: "a", Source: "test",
	}).Error)
	svc := &knowledgeService{repo: repository.NewKnowledgeRepository(db)}
	ctx := context.Background()
	first, replay, err := svc.PrepareReparseSubmission(ctx, 7, "kb", "", []string{"a"}, nil, "same-key")
	require.NoError(t, err)
	require.False(t, replay)
	second, replay, err := svc.PrepareReparseSubmission(ctx, 7, "kb", "", []string{"a"}, nil, "same-key")
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, first.BatchID, second.BatchID)
	require.Equal(t, first.TaskID, second.TaskID)
	_, _, err = svc.PrepareReparseSubmission(
		ctx, 7, "kb", "", []string{"a"}, &types.KnowledgeProcessOverrides{}, "same-key",
	)
	require.True(t, errors.Is(err, ErrReparseSubmissionConflict))
}
