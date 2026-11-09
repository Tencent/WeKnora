package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type cloneCommitV4Files struct {
	interfaces.FileService
	objects         map[string][]byte
	copied, deleted []string
}

func (f *cloneCommitV4Files) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := f.objects[path]
	if !ok {
		return nil, errors.New("file missing")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *cloneCommitV4Files) SaveBytes(_ context.Context, data []byte, _ uint64, name string, _ bool) (string, error) {
	path := "local://copied/" + name
	f.objects[path] = append([]byte(nil), data...)
	f.copied = append(f.copied, path)
	return path, nil
}

func (f *cloneCommitV4Files) DeleteFile(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	delete(f.objects, path)
	return nil
}

// The real repository persists rows; this wrapper injects transport outcomes
// and temporarily missing reads around that commit boundary.
type cloneCommitV4Repo struct {
	interfaces.KnowledgeRepository
	insertErr                        error
	commitBeforeReply                bool
	hiddenReads                      int
	readErr                          error
	destination                      *types.Knowledge
	insertAttempts, destinationReads int
}

func (r *cloneCommitV4Repo) SnapshotKnowledgeFolderPath(
	_ context.Context,
	tenant uint64,
	kb,
	path string,
) (*types.KnowledgeFolderPathSnapshot, error) {
	return &types.KnowledgeFolderPathSnapshot{TenantID: tenant, KnowledgeBaseID: kb, Path: path}, nil
}

func (r *cloneCommitV4Repo) CreateKnowledgeWithFolderSnapshot(
	ctx context.Context,
	row *types.Knowledge,
	_ *types.KnowledgeFolderPathSnapshot,
) error {
	return r.CreateKnowledge(ctx, row)
}

func (r *cloneCommitV4Repo) CreateKnowledge(ctx context.Context, row *types.Knowledge) error {
	r.insertAttempts++
	copied := *row
	r.destination = &copied
	if r.commitBeforeReply {
		if err := r.KnowledgeRepository.CreateKnowledge(ctx, row); err != nil {
			return err
		}
	}
	return r.insertErr
}

func (r *cloneCommitV4Repo) GetKnowledgeByID(ctx context.Context, tenant uint64, id string) (*types.Knowledge, error) {
	if r.destination != nil && id == r.destination.ID {
		r.destinationReads++
		if r.destinationReads <= r.hiddenReads {
			return nil, r.readErr
		}
	}
	return r.KnowledgeRepository.GetKnowledgeByID(ctx, tenant, id)
}

func TestCloneV4CommitUncertaintyPreservesCopiedSource(t *testing.T) {
	for _, scenario := range []struct {
		name              string
		insertErr         error
		commitBeforeReply bool
		hiddenReads       int
		readErr           error
	}{
		{
			"late commit after lost acknowledgement",
			errors.New("commit acknowledgement lost"),
			false,
			1,
			repository.ErrKnowledgeNotFound,
		},
		{
			"committed but acknowledgement and visibility lost",
			errors.New("commit acknowledgement lost"),
			true,
			1,
			repository.ErrKnowledgeNotFound,
		},
		{"successful insert followed by missing reads", nil, true, 2, repository.ErrKnowledgeNotFound},
		{
			"uncertain insert and unavailable read",
			errors.New("commit acknowledgement lost"),
			false,
			1,
			errors.New("read unavailable"),
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferClone)
			files := &cloneCommitV4Files{objects: map[string][]byte{"source-original.md": []byte("original content")}}
			f.svc.fileSvc = files
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
				Updates(map[string]interface{}{"file_path": "source-original.md", "folder_path": "docs"}).Error)
			src, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			faults := &cloneCommitV4Repo{
				KnowledgeRepository: f.repo, insertErr: scenario.insertErr,
				commitBeforeReply: scenario.commitBeforeReply,
				hiddenReads:       scenario.hiddenReads, readErr: scenario.readErr,
			}
			f.svc.repo = faults
			err = f.svc.cloneKnowledge(f.ctx, src, f.kbs.values["other"])
			require.Error(t, err)
			require.Equal(t, 1, faults.insertAttempts)
			require.Len(t, files.copied, 1)
			require.Empty(t, files.deleted, "missing or unavailable reads do not prove that the insert cannot commit")
			require.Contains(t, files.objects, files.copied[0])
			require.Contains(t, files.objects, "source-original.md")
			if !scenario.commitBeforeReply {
				// Model the original INSERT becoming visible after the failed
				// request and its cleanup read have already returned.
				require.NoError(t, f.repo.CreateKnowledge(f.ctx, faults.destination))
			}
			stored, err := f.repo.GetKnowledgeByID(f.ctx, 7, faults.destination.ID)
			require.NoError(t, err)
			require.Equal(t, files.copied[0], stored.FilePath)
			require.Equal(
				t,
				[]byte("original content"),
				files.objects[stored.FilePath],
				"a later-visible row must still have its copied source",
			)
		})
	}
}

func TestCloneV4KnownPreinsertRejectionCleansOnlyConfirmedAbsentCopy(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		insertErr, readErr error
		wantCode           int
		wantCleanup        bool
	}{
		{
			"folder changed",
			fmt.Errorf("placement rejected: %w", repository.ErrKnowledgeFolderChanged),
			repository.ErrKnowledgeNotFound,
			409,
			true,
		},
		{
			"invalid folder",
			fmt.Errorf("placement rejected: %w", repository.ErrKnowledgeFolderInvalidPath),
			repository.ErrKnowledgeNotFound,
			400,
			true,
		},
		{
			"known rejection with unreadable result",
			repository.ErrKnowledgeFolderChanged,
			errors.New("read unavailable"),
			409,
			false,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferClone)
			files := &cloneCommitV4Files{objects: map[string][]byte{"source-original.md": []byte("original content")}}
			f.svc.fileSvc = files
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
				Updates(map[string]interface{}{"file_path": "source-original.md", "folder_path": "docs"}).Error)
			src, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			faults := &cloneCommitV4Repo{
				KnowledgeRepository: f.repo,
				insertErr:           scenario.insertErr,
				hiddenReads:         1,
				readErr:             scenario.readErr,
			}
			f.svc.repo = faults
			err = f.svc.cloneKnowledge(f.ctx, src, f.kbs.values["other"])
			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, scenario.wantCode, appErr.HTTPCode)
			require.False(
				t,
				errors.Is(err, repository.ErrKnowledgeFolderChanged),
				"AppError mapping loses the repository sentinel",
			)
			require.Len(t, files.copied, 1)
			if scenario.wantCleanup {
				require.Equal(t, files.copied, files.deleted)
				require.NotContains(t, files.objects, files.copied[0])
			} else {
				require.Empty(t, files.deleted)
				require.Contains(t, files.objects, files.copied[0])
			}
			require.Contains(t, files.objects, "source-original.md")
			_, err = f.repo.GetKnowledgeByID(f.ctx, 7, faults.destination.ID)
			require.ErrorIs(t, err, repository.ErrKnowledgeNotFound)
		})
	}
}
