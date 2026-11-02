package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type folderV2RenameErrorRepo struct{ folderMoveRepoStub }

func (r *folderV2RenameErrorRepo) RenameKnowledgeFolderPath(
	context.Context,
	uint64,
	string,
	string,
	string,
) (int64, error) {
	return 0, fmt.Errorf("wrapped descendant validation: %w", repository.ErrKnowledgeFolderInvalidPath)
}

func TestKnowledgeFolderV2RenameMapsDescendantLimitTo400(t *testing.T) {
	repo := &folderV2RenameErrorRepo{}
	svc := &knowledgeService{repo: repo, kbService: &writeKBLookup{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}}}
	_, err := svc.RenameKnowledgeFolder(folderMoveContext(), "kb-1", "old", "new")
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 400, appErr.HTTPCode)
}

func TestKnowledgeFolderV2RenameNeverTruncatesRequest(t *testing.T) {
	repo := &folderMoveRepoStub{}
	svc := &knowledgeService{repo: repo, kbService: &writeKBLookup{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}}}
	for _, path := range []string{
		strings.Repeat("level/", 16) + "leaf",
		strings.Repeat("界", 43),
		strings.Repeat(strings.Repeat("a", 128)+"/", 7) + strings.Repeat("b", 128),
	} {
		for _, invalidSource := range []bool{false, true} {
			from, to := "old", path
			if invalidSource {
				from, to = path, "new"
			}
			_, err := svc.RenameKnowledgeFolder(folderMoveContext(), "kb-1", from, to)
			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, 400, appErr.HTTPCode)
		}
	}
	require.Zero(t, repo.renameCalls, "invalid path must be rejected before a write")
	_, err := svc.RenameKnowledgeFolder(folderMoveContext(), "kb-1", "/old/", " new//child/ ")
	require.NoError(t, err)
	require.Equal(t, "old", repo.renameFrom)
	require.Equal(t, "new/child", repo.renameTo)
}
