package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type expectedFileVersionKey struct{}

func (s *knowledgeService) UploadKnowledgeFileVersion(
	ctx context.Context, id string, file *multipart.FileHeader, expected int,
) (*types.Knowledge, error) {
	if expected < 0 {
		return nil, werrors.NewBadRequestError("invalid expected_version")
	}
	ctx = context.WithValue(ctx, expectedFileVersionKey{}, expected)
	return s.ReplaceKnowledgeFile(ctx, id, file, "", nil)
}

func (s *knowledgeService) ListKnowledgeFileVersions(
	ctx context.Context, id string, limit, offset int,
) (*types.KnowledgeFileVersionList, error) {
	tenantID, err := writeExecutionTenant(ctx)
	if err != nil {
		return nil, err
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if knowledge.Type != "file" || knowledge.FilePath == "" {
		return nil, werrors.NewBadRequestError("only file knowledge has file versions")
	}
	repo, ok := s.repo.(interfaces.KnowledgeFileVersionRepository)
	if !ok {
		return nil, werrors.NewServiceUnavailableError("file versions unavailable")
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, werrors.NewBadRequestError("invalid pagination")
	}
	archiveLimit, archiveOffset := limit, offset
	items := make([]*types.KnowledgeFileVersion, 0, limit)
	if offset == 0 {
		current := knowledge.FileVersionSnapshot()
		current.IsCurrent = true
		items = append(items, current)
		archiveLimit--
	} else {
		archiveOffset--
	}
	rows, total, err := repo.ListKnowledgeFileVersions(ctx, knowledge.TenantID, id, archiveLimit, archiveOffset)
	if err != nil {
		return nil, err
	}
	latest, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if latest.CurrentFileVersion() != knowledge.CurrentFileVersion() || latest.FilePath != knowledge.FilePath {
		return nil, werrors.NewConflictError("file version changed while listing; refresh and retry")
	}
	items = append(items, rows...)
	return &types.KnowledgeFileVersionList{Items: items, Total: total + 1}, nil
}

func (s *knowledgeService) GetKnowledgeFileVersion(
	ctx context.Context, id string, version int,
) (io.ReadCloser, string, error) {
	// Historical original files have the same write-grant boundary as current
	// original downloads, including shared knowledge bases.
	knowledge, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, id)
	if err != nil {
		return nil, "", err
	}
	if version < 1 || knowledge.Type != "file" || knowledge.FilePath == "" {
		return nil, "", werrors.NewBadRequestError("invalid file version")
	}
	row := knowledge.FileVersionSnapshot()
	if version != row.Version {
		repo, ok := s.repo.(interfaces.KnowledgeFileVersionRepository)
		if !ok {
			return nil, "", werrors.NewServiceUnavailableError("file versions unavailable")
		}
		row, err = repo.GetKnowledgeFileVersion(ctx, knowledge.TenantID, id, version)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", werrors.NewNotFoundError("file version not found")
		}
		if err != nil {
			return nil, "", err
		}
	}
	file, err := s.resolveFileServiceForPath(ctx, kb, row.FilePath).GetFile(ctx, row.FilePath)
	return file, row.FileName, err
}

func mapFileVersionError(err error) error {
	if errors.Is(err, repository.ErrKnowledgeFileVersionConflict) {
		return werrors.NewConflictError(err.Error())
	}
	return err
}

// cleanupKnowledgeFileVersions removes retained originals and, only when every
// object is gone, the manifest. A missing object counts as success so a retry
// can finish the rest. Any other failure keeps the manifest and is returned:
// knowledge-base deletion calls this before the knowledge rows are removed, so
// the task can be retried while those rows still identify the versions.
func cleanupKnowledgeFileVersions(
	ctx context.Context, repo interfaces.KnowledgeRepository, tenantID uint64, id string,
	resolve func(string) interfaces.FileService,
) error {
	versions, ok := repo.(interfaces.KnowledgeFileVersionRepository)
	if !ok {
		return nil
	}
	var failed error
	for offset := 0; ; offset += 100 {
		rows, total, err := versions.ListKnowledgeFileVersions(ctx, tenantID, id, 100, offset)
		if err != nil {
			return err
		}
		for _, row := range rows {
			fileSvc := resolve(row.FilePath)
			if fileSvc == nil {
				failed = errors.Join(failed, fmt.Errorf("no file service for %s", row.FilePath))
				continue
			}
			if err := fileSvc.DeleteFile(ctx, row.FilePath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				logger.Warnf(ctx, "Failed to delete historical file for %s: %v", id, err)
				failed = errors.Join(failed, err)
			}
		}
		if len(rows) == 0 || int64(offset+len(rows)) >= total {
			break
		}
	}
	if failed != nil {
		return failed
	}
	return versions.DeleteKnowledgeFileVersions(ctx, tenantID, id)
}
