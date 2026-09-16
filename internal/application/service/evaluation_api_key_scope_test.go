package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func evaluationTaskWithSourceKnowledgeBase(t *testing.T, source string) *types.EvaluationTaskEntity {
	t.Helper()
	experiment, err := json.Marshal(types.EvaluationExperimentSnapshot{SourceKnowledgeBaseID: &source})
	require.NoError(t, err)
	datasetVersionID := "version-1"
	contentHash := strings.Repeat("a", 64)
	experimentHash := strings.Repeat("b", 64)
	return &types.EvaluationTaskEntity{
		ExperimentSnapshot:   experiment,
		DatasetVersionID:     &datasetVersionID,
		DatasetContentSHA256: &contentHash,
		ExperimentSHA256:     &experimentHash,
	}
}

func TestAuthorizeEvaluationTaskForAPIKeyUsesFrozenSourceKnowledgeBase(t *testing.T) {
	restricted := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	require.NoError(t, AuthorizeEvaluationTaskForAPIKey(
		restricted, evaluationTaskWithSourceKnowledgeBase(t, "kb-allowed")))
	require.ErrorIs(t, AuthorizeEvaluationTaskForAPIKey(
		restricted, evaluationTaskWithSourceKnowledgeBase(t, "kb-denied")),
		interfaces.ErrEvaluationTaskNotFound)
	require.ErrorIs(t, AuthorizeEvaluationTaskForAPIKey(
		restricted, &types.EvaluationTaskEntity{}),
		interfaces.ErrEvaluationTaskNotFound)
	require.NoError(t, AuthorizeEvaluationTaskForAPIKey(
		context.Background(), &types.EvaluationTaskEntity{}))
}
