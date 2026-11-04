package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type replacementGenerationProbe struct {
	documentTestScheduler
	interfaces.DocumentGenerationRepository
	row             *replaceFileRepo
	token           string
	begins          int
	beginSource     string
	guardedWrites   int
	supersedeOnSave bool
}

func (p *replacementGenerationProbe) BeginDocumentGeneration(
	_ context.Context, _ uint64, _, _, _ string,
) (string, error) {
	p.begins++
	p.beginSource = p.row.row.FilePath
	p.token = "replacement-generation"
	return p.token, nil
}

func (p *replacementGenerationProbe) InitialDocumentGeneration(
	context.Context, uint64, string, string,
) (string, error) {
	return p.token, nil
}

func (p *replacementGenerationProbe) CheckDocumentGeneration(
	_ context.Context, _ uint64, _, token string,
) (bool, error) {
	return token == p.token, nil
}

func (p *replacementGenerationProbe) EnqueueDocumentGeneration(
	_ context.Context, op *types.TaskPendingOp, token string,
) (bool, error) {
	if token != p.token {
		return false, nil
	}
	op.ID = 73
	p.op = op
	return true, nil
}

func (p *replacementGenerationProbe) UpdateDocumentGeneration(
	ctx context.Context, _ uint64, knowledgeID, token string, columns map[string]any,
) (bool, error) {
	p.guardedWrites++
	if token != p.token {
		return false, nil
	}
	return true, p.row.UpdateKnowledgeColumns(ctx, knowledgeID, columns)
}

func (p *replacementGenerationProbe) SaveDocumentGeneration(
	ctx context.Context, token string, knowledge *types.Knowledge,
) (bool, error) {
	if p.supersedeOnSave {
		p.supersedeOnSave = false
		p.token = "newer-generation"
		p.row.row.FilePath = "winner/file.md"
		p.row.row.Title = "newer replacement"
		p.row.row.ParseStatus = types.ParseStatusPending
		return false, nil
	}
	if token != p.token {
		return false, nil
	}
	return true, p.row.UpdateKnowledge(ctx, knowledge)
}

func TestDocumentReplacementGenerationPrecedesSourceMutationAndIsReused(t *testing.T) {
	h := newReplaceFileHarness(t)
	p := &replacementGenerationProbe{row: h.repo}
	h.svc.taskPendingRepo = p
	_, err := h.replace(t, "new source", "a.md", nil)
	require.NoError(t, err)
	require.Equal(t, h.original.FilePath, p.beginSource, "invalidate the old owner before publishing the new source")
	require.Equal(t, 1, p.begins, "nested reparse must reuse the replacement token")
	require.Positive(t, p.guardedWrites)
	require.Equal(t, "new/file.md", h.repo.row.FilePath)
}

func TestDocumentReplacementRollbackCannotRestoreOverNewerGeneration(t *testing.T) {
	h := newReplaceFileHarness(t)
	p := &replacementGenerationProbe{row: h.repo, supersedeOnSave: true}
	h.svc.taskPendingRepo = p
	_, err := h.replace(t, "new source", "a.md", nil)
	require.Error(t, err)
	require.Equal(t, "winner/file.md", h.repo.row.FilePath)
	require.Equal(t, "newer replacement", h.repo.row.Title)
	require.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
	require.NotContains(t, h.store.deleted, "winner/file.md")
}

func TestDocumentReplacementSourceFailureSettlesInvalidatedGeneration(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.row.ParseStatus = types.ParseStatusProcessing
	h.repo.failColumnsCall = 1
	p := &replacementGenerationProbe{row: h.repo}
	h.svc.taskPendingRepo = p
	_, err := h.replace(t, "new source", "a.md", nil)
	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, types.ParseStatusFailed, h.repo.row.ParseStatus,
		"the invalidated prior parse cannot resume after replacement setup fails")
	require.Contains(t, h.repo.row.ErrorMessage, "database unavailable")
	require.Equal(t, h.original.FilePath, h.repo.row.FilePath)
	require.Nil(t, p.op)
}
