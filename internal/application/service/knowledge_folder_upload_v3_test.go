package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type uploadV3Repo struct {
	createKnowledgeFileRepoStub
	snapshotErr error
	snapshots   int
	anchor      string
	present     bool
}

func (r *uploadV3Repo) SnapshotKnowledgeFolderPath(
	ctx context.Context,
	tenant uint64,
	kb,
	path string,
) (*types.KnowledgeFolderPathSnapshot, error) {
	r.snapshots++
	r.anchor, r.present = types.KnowledgeFolderAnchorFromContext(ctx)
	if r.snapshotErr != nil {
		return nil, r.snapshotErr
	}
	return &types.KnowledgeFolderPathSnapshot{TenantID: tenant, KnowledgeBaseID: kb, Path: path}, nil
}

func TestFolderUploadV3AnchorConflictBeforeOrAfterStorage(t *testing.T) {
	for _, before := range []bool{true, false} {
		r := &uploadV3Repo{}
		if before {
			r.snapshotErr = repository.ErrKnowledgeFolderChanged
		} else {
			r.createErr = repository.ErrKnowledgeFolderChanged
		}
		fs := &createKnowledgeFileServiceStub{}
		s := &knowledgeService{
			repo:      r,
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
			fileSvc:   fs,
		}
		_, err := s.CreateKnowledgeFromFile(
			types.WithKnowledgeFolderAnchor(newCreateKnowledgeFileContext(), "selected"),
			"kb-1",
			newMultipartFileHeader(t, "a.md", "hello"),
			nil,
			nil,
			"selected/new/a.md",
			nil,
			"",
			nil,
		)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, 409, appErr.HTTPCode)
		require.Equal(t, 1, r.snapshots)
		require.True(t, r.present)
		require.Equal(t, "selected", r.anchor)
		if before {
			require.Zero(t, fs.saveCalls)
			require.Zero(t, fs.deleteCalls)
		} else {
			require.Equal(t, 1, fs.saveCalls)
			require.Equal(t, 1, fs.deleteCalls)
			require.Equal(t, "stored/"+fs.savedWithKnowledgeID, fs.deletedPath)
		}
	}
}

func TestFolderUploadV3RejectsOversizeRelativePathBeforeStorage(t *testing.T) {
	r := &uploadV3Repo{}
	fs := &createKnowledgeFileServiceStub{}
	s := &knowledgeService{
		repo:      r,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   fs,
	}
	_, err := s.CreateKnowledgeFromFile(
		types.WithKnowledgeFolderAnchor(newCreateKnowledgeFileContext(), ""),
		"kb-1",
		newMultipartFileHeader(t, "a.md", "hello"),
		nil,
		nil,
		strings.Repeat("level/", 17)+"a.md",
		nil,
		"",
		nil,
	)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 400, appErr.HTTPCode)
	require.Zero(t, fs.saveCalls)
	require.Zero(t, r.createCalls)
}
