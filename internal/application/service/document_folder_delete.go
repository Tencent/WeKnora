package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type documentFolderDeleteSnapshot struct {
	folderIDs    []string
	knowledgeIDs []string
	activeCount  int
}

// DeleteFolderTree executes the destructive portion of a submitted folder
// deletion. The asynchronous wrapper is responsible for retries and tenant
// context restoration.
func (s *documentFolderService) DeleteFolderTree(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	id string,
	mode types.DocumentFolderDeleteMode,
) error {
	switch mode {
	case types.DocumentFolderDeleteModeKeepDocuments:
		return s.deleteFolderTreeKeepingDocuments(ctx, kbID, tenantID, id)
	case types.DocumentFolderDeleteModeDeleteAll:
		return s.deleteFolderTreeWithDocuments(ctx, kbID, tenantID, id)
	default:
		return fmt.Errorf("unknown document folder delete mode %q", mode)
	}
}

func (s *documentFolderService) deleteFolderTreeWithDocuments(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	id string,
) error {
	if s.knowledgeService == nil {
		return fmt.Errorf("knowledge service is unavailable")
	}
	planned, err := s.loadDeleteSnapshot(ctx, s.repo, kbID, tenantID, id)
	if err != nil {
		return err
	}

	// Reuse the single-document deletion path so each item resolves the vector
	// store bound to this KB before cleaning files, chunks, vectors, graph data,
	// and relational rows. A retry is safe: the next snapshot only contains
	// documents that remain after any partially completed attempt.
	for _, knowledgeID := range planned.knowledgeIDs {
		if err := s.knowledgeService.DeleteKnowledge(ctx, knowledgeID); err != nil &&
			!errors.Is(err, repository.ErrKnowledgeNotFound) {
			return fmt.Errorf("delete folder document %s: %w", knowledgeID, err)
		}
	}

	return s.repo.UpdateFoldersInTransaction(ctx, kbID, func(txRepo interfaces.DocumentFolderRepository) error {
		current, err := s.loadDeleteSnapshot(ctx, txRepo, kbID, tenantID, id)
		if err != nil {
			return err
		}
		if !sameStringSet(planned.folderIDs, current.folderIDs) || len(current.knowledgeIDs) > 0 {
			return ErrFolderChangedDuringDelete
		}
		for index := len(current.folderIDs) - 1; index >= 0; index-- {
			if err := txRepo.DeleteFolder(ctx, kbID, current.folderIDs[index]); err != nil {
				return fmt.Errorf("delete folder %s: %w", current.folderIDs[index], err)
			}
		}
		return nil
	})
}

func (s *documentFolderService) deleteFolderTreeKeepingDocuments(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	id string,
) error {
	if s.knowledgeService == nil {
		return fmt.Errorf("knowledge service is unavailable")
	}
	planned, err := s.loadDeleteSnapshot(ctx, s.repo, kbID, tenantID, id)
	if err != nil {
		return err
	}
	if planned.activeCount > 0 {
		return ErrFolderDocumentsProcessing
	}

	// Update the denormalized index payload first. If the subsequent locked
	// transaction detects a concurrent change, the worker retries; retrieval's
	// relational post-filter keeps this temporary index-ahead state fail-closed.
	if err := s.knowledgeService.UpdateKnowledgeFolderIndex(
		ctx, kbID, planned.knowledgeIDs, types.DocumentFolderRootID,
	); err != nil {
		return fmt.Errorf("move folder index payloads to root: %w", err)
	}

	return s.repo.UpdateFoldersInTransaction(ctx, kbID, func(txRepo interfaces.DocumentFolderRepository) error {
		current, err := s.loadDeleteSnapshot(ctx, txRepo, kbID, tenantID, id)
		if err != nil {
			return err
		}
		if current.activeCount > 0 {
			return ErrFolderDocumentsProcessing
		}
		if !sameStringSet(planned.folderIDs, current.folderIDs) ||
			!sameStringSet(planned.knowledgeIDs, current.knowledgeIDs) {
			return ErrFolderChangedDuringDelete
		}
		if len(current.knowledgeIDs) > 0 {
			affected, err := txRepo.SetKnowledgeFolderID(
				ctx, tenantID, kbID, current.knowledgeIDs, types.DocumentFolderRootID,
			)
			if err != nil {
				return fmt.Errorf("move folder documents to root: %w", err)
			}
			if affected != int64(len(current.knowledgeIDs)) {
				return ErrFolderChangedDuringDelete
			}
		}
		for index := len(current.folderIDs) - 1; index >= 0; index-- {
			if err := txRepo.DeleteFolder(ctx, kbID, current.folderIDs[index]); err != nil {
				return fmt.Errorf("delete folder %s: %w", current.folderIDs[index], err)
			}
		}
		return nil
	})
}

func (s *documentFolderService) loadDeleteSnapshot(
	ctx context.Context,
	repo interfaces.DocumentFolderRepository,
	kbID string,
	tenantID uint64,
	id string,
) (*documentFolderDeleteSnapshot, error) {
	allFolders, err := repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list folders for deletion: %w", err)
	}
	folderIDs, err := resolveSubtreeFolderIDs(allFolders, id)
	if err != nil {
		return nil, err
	}
	knowledges, err := repo.ListKnowledgeInFolders(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return nil, fmt.Errorf("list documents for deletion: %w", err)
	}
	knowledgeIDs := make([]string, 0, len(knowledges))
	activeCount := 0
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		knowledgeIDs = append(knowledgeIDs, knowledge.ID)
		if isActiveFolderDocumentParse(knowledge.ParseStatus) {
			activeCount++
		}
	}
	return &documentFolderDeleteSnapshot{
		folderIDs:    folderIDs,
		knowledgeIDs: knowledgeIDs,
		activeCount:  activeCount,
	}, nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}
