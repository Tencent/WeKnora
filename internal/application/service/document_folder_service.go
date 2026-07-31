package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// documentFolderService implements interfaces.DocumentFolderService. It owns
// the validation/business-rule layer for the folder tree. The HTTP handler
// depends on this interface; persistence is delegated to
// interfaces.DocumentFolderRepository.
type documentFolderService struct {
	repo             interfaces.DocumentFolderRepository
	knowledgeService interfaces.KnowledgeService
	tenantRepo       interfaces.TenantRepository
	task             interfaces.TaskEnqueuer
}

// NewDocumentFolderService constructs a DocumentFolderService backed by repo.
func NewDocumentFolderService(
	repo interfaces.DocumentFolderRepository,
	knowledgeService interfaces.KnowledgeService,
	tenantRepo interfaces.TenantRepository,
	task interfaces.TaskEnqueuer,
) interfaces.DocumentFolderService {
	return &documentFolderService{
		repo:             repo,
		knowledgeService: knowledgeService,
		tenantRepo:       tenantRepo,
		task:             task,
	}
}

// ListFolders returns the direct children of parentID, enriched with the live
// document count directly under each folder and whether each has child
// folders. parentID == "" returns the root-level listing.
func (s *documentFolderService) ListFolders(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	parentID string,
	keyword string,
	cursor string,
	pageSize int,
) (*types.DocumentFolderListResponse, error) {
	after, err := parseDocumentFolderCursor(cursor)
	if err != nil {
		return nil, err
	}
	pageSize = normalizeDocumentFolderPageSize(pageSize)

	children, hasMore, err := s.repo.ListChildFolders(
		ctx,
		kbID,
		parentID,
		strings.TrimSpace(keyword),
		after,
		pageSize,
	)
	if err != nil {
		return nil, fmt.Errorf("list child folders: %w", err)
	}

	// Batch-load document counts and child-presence in two grouped passes to
	// avoid an N+1 listing penalty on wide folders.
	folderIDs := make([]string, 0, len(children))
	for _, f := range children {
		folderIDs = append(folderIDs, f.ID)
	}
	docCounts, err := s.repo.CountDocumentsInFolders(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return nil, fmt.Errorf("count documents in folders: %w", err)
	}
	childPresence, err := s.repo.HasChildFoldersBatch(ctx, kbID, folderIDs)
	if err != nil {
		return nil, fmt.Errorf("check child folders: %w", err)
	}

	nodes := make([]types.DocumentFolderNode, 0, len(children))
	for _, f := range children {
		nodes = append(nodes, types.DocumentFolderNode{
			DocumentFolder: *f,
			DocumentCount:  docCounts[f.ID],
			HasChildren:    childPresence[f.ID],
		})
	}

	nextCursor := ""
	if hasMore {
		nextCursor, err = encodeDocumentFolderCursor(children[len(children)-1])
		if err != nil {
			return nil, fmt.Errorf("encode folder cursor: %w", err)
		}
	}
	return &types.DocumentFolderListResponse{
		ParentID:   parentID,
		Folders:    nodes,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func parseDocumentFolderCursor(cursor string) (*types.DocumentFolderPageCursor, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, ErrFolderCursorInvalid
	}
	var after types.DocumentFolderPageCursor
	if err := json.Unmarshal(payload, &after); err != nil || after.Name == "" || after.ID == "" {
		return nil, ErrFolderCursorInvalid
	}
	return &after, nil
}

func encodeDocumentFolderCursor(folder *types.DocumentFolder) (string, error) {
	payload, err := json.Marshal(types.DocumentFolderPageCursor{
		Name: folder.Name,
		ID:   folder.ID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func normalizeDocumentFolderPageSize(pageSize int) int {
	if pageSize <= 0 {
		return types.DefaultDocumentFolderPageSize
	}
	if pageSize > types.MaxDocumentFolderPageSize {
		return types.MaxDocumentFolderPageSize
	}
	return pageSize
}

// CreateFolder creates a new (initially empty) folder under parentID
// ("" = root). Validates the name, enforces MaxFoldersPerKB on the KB and
// MaxFolderDepth on the new folder's depth. The cap check, uniqueness probe,
// and insert run inside a single transaction so two concurrent creates with
// the same name or at the cap boundary cannot both succeed.
func (s *documentFolderService) CreateFolder(
	ctx context.Context, kbID string, tenantID uint64, parentID string, name string,
) (*types.DocumentFolder, error) {
	name, err := validateDocumentFolderName(name)
	if err != nil {
		return nil, err
	}

	var folder *types.DocumentFolder
	if err := s.repo.UpdateFoldersInTransaction(ctx, kbID, func(txRepo interfaces.DocumentFolderRepository) error {
		parentPath := ""
		depth := 1
		if parentID != types.DocumentFolderRootID {
			parent, err := txRepo.GetFolderByID(ctx, kbID, parentID)
			if err != nil {
				return err
			}
			parentPath = parent.Path
			depth = parent.Depth + 1
		}
		if depth > types.MaxFolderDepth {
			return ErrFolderDepthExceeded
		}

		path := name
		if parentPath != "" {
			path = parentPath + "/" + name
		}
		if len(path) > types.MaxFolderPathLen {
			return ErrFolderDepthExceeded
		}

		count, err := txRepo.CountAllFolders(ctx, kbID)
		if err != nil {
			return fmt.Errorf("count folders: %w", err)
		}
		if count >= int64(types.MaxFoldersPerKB) {
			return ErrFolderLimitExceeded
		}
		// Sibling uniqueness probe inside the transaction.
		if _, err := txRepo.GetChildFolderByName(ctx, kbID, parentID, name); err == nil {
			return ErrFolderConflict
		} else if !isRepoNotFound(err) {
			return err
		}
		now := time.Now()
		folder = &types.DocumentFolder{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			ParentID:        parentID,
			Name:            name,
			Path:            path,
			Depth:           depth,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return txRepo.CreateFolder(ctx, folder)
	}); err != nil {
		return nil, err
	}
	return folder, nil
}

// RenameFolder renames a folder and rewrites the materialized paths of all its
// descendants. parent_id and depth are stable because document folders cannot
// be reparented.
func (s *documentFolderService) RenameFolder(
	ctx context.Context, kbID string, id string, newName string,
) (*types.DocumentFolder, error) {
	name, err := validateDocumentFolderName(newName)
	if err != nil {
		return nil, err
	}

	var updated *types.DocumentFolder
	if err := s.repo.UpdateFoldersInTransaction(ctx, kbID, func(txRepo interfaces.DocumentFolderRepository) error {
		folder, err := txRepo.GetFolderByID(ctx, kbID, id)
		if err != nil {
			return err
		}
		if name == folder.Name {
			updated = folder
			return nil
		}

		if existing, err := txRepo.GetChildFolderByName(ctx, kbID, folder.ParentID, name); err == nil {
			if existing.ID != folder.ID {
				return ErrFolderConflict
			}
		} else if !isRepoNotFound(err) {
			return err
		}

		oldPath := folder.Path
		newPath := name
		if separator := strings.LastIndex(oldPath, "/"); separator >= 0 {
			newPath = oldPath[:separator+1] + name
		}

		all, err := txRepo.ListAllFolders(ctx, kbID)
		if err != nil {
			return fmt.Errorf("list all folders: %w", err)
		}

		now := time.Now()
		pending := make([]*types.DocumentFolder, 0)
		for _, current := range all {
			cp := *current
			switch {
			case cp.ID == folder.ID:
				cp.Name = name
				cp.Path = newPath
			case strings.HasPrefix(cp.Path, oldPath+"/"):
				cp.Path = newPath + cp.Path[len(oldPath):]
			default:
				continue
			}
			if len(cp.Path) > types.MaxFolderPathLen {
				return ErrFolderDepthExceeded
			}
			cp.UpdatedAt = now
			pending = append(pending, &cp)
			if cp.ID == folder.ID {
				rootCopy := cp
				updated = &rootCopy
			}
		}
		if updated == nil {
			return repository.ErrDocumentFolderNotFound
		}

		for _, pendingFolder := range pending {
			if err := txRepo.UpdateFolder(ctx, pendingFolder); err != nil {
				return fmt.Errorf("update folder %s: %w", pendingFolder.ID, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteFolder soft-deletes a folder that has no live descendants and no live
// documents filed anywhere in its subtree. Refuses otherwise. The empty checks
// and the delete run inside a single KB-locked transaction so a concurrent
// child-folder or document creation cannot leave an orphan.
func (s *documentFolderService) DeleteFolder(ctx context.Context, kbID string, id string) error {
	return s.repo.UpdateFoldersInTransaction(ctx, kbID, func(txRepo interfaces.DocumentFolderRepository) error {
		folder, err := txRepo.GetFolderByID(ctx, kbID, id)
		if err != nil {
			return err
		}
		// Direct children check — fast path.
		has, err := txRepo.HasChildFolders(ctx, kbID, id)
		if err != nil {
			return fmt.Errorf("check child folders: %w", err)
		}
		if has {
			return ErrFolderNotEmpty
		}

		// Subtree documents: because we just verified no direct child folders,
		// the subtree is exactly {folder}. We still probe documents in the
		// direct bucket for defense-in-depth.
		if has, err = txRepo.HasDocumentsInSubtree(ctx, folder.TenantID, kbID, []string{id}); err != nil {
			return fmt.Errorf("check documents in subtree: %w", err)
		} else if has {
			return ErrFolderNotEmpty
		}

		return txRepo.DeleteFolder(ctx, kbID, id)
	})
}

// GetDeleteImpact counts the complete live subtree and classifies documents
// whose parser could still write the old folder ID into an index payload.
func (s *documentFolderService) GetDeleteImpact(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	id string,
) (*types.DocumentFolderDeleteImpact, error) {
	subtreeIDs, err := s.ResolveSubtreeFolderIDs(ctx, kbID, id)
	if err != nil {
		return nil, err
	}
	knowledges, err := s.repo.ListKnowledgeInFolders(ctx, tenantID, kbID, subtreeIDs)
	if err != nil {
		return nil, fmt.Errorf("list documents in folder subtree: %w", err)
	}
	active := 0
	for _, knowledge := range knowledges {
		if knowledge != nil && isActiveFolderDocumentParse(knowledge.ParseStatus) {
			active++
		}
	}
	return &types.DocumentFolderDeleteImpact{
		FolderCount:         len(subtreeIDs),
		DocumentCount:       len(knowledges),
		ActiveDocumentCount: active,
	}, nil
}

func isActiveFolderDocumentParse(status string) bool {
	switch status {
	case types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing:
		return true
	default:
		return false
	}
}

// ResolveSubtreeFolderIDs is the L3 query-time BFS expansion. It loads the KB
// tree once and returns rootID + every descendant. Used by the retrieval
// pipeline to build the folder_id IN (...) filter for folder-scoped Q&A.
func (s *documentFolderService) ResolveSubtreeFolderIDs(
	ctx context.Context, kbID string, folderID string,
) ([]string, error) {
	// Quick existence check first so we return ErrFolderNotFound, not
	// ErrFolderCycleInData, for an unknown root.
	if _, err := s.repo.GetFolderByID(ctx, kbID, folderID); err != nil {
		return nil, err
	}
	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list all folders: %w", err)
	}
	return resolveSubtreeFolderIDs(all, folderID)
}

// ValidateFolderExistsForUpload is the lightweight membership guard used by
// the document upload / create paths. Returns nil if folderID is "" (root) or
// if the folder exists and is not deleted; otherwise a sentinel error.
func (s *documentFolderService) ValidateFolderExistsForUpload(
	ctx context.Context, kbID string, folderID string,
) error {
	if folderID == types.DocumentFolderRootID {
		return nil
	}
	_, err := s.repo.GetFolderByID(ctx, kbID, folderID)
	return err
}

func (s *documentFolderService) SearchFolders(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset int,
	limit int,
) ([]*types.DocumentFolderSearchResult, bool, int64, error) {
	if offset < 0 {
		return nil, false, 0, ErrFolderCursorInvalid
	}
	limit = normalizeDocumentFolderPageSize(limit)
	return s.repo.SearchFoldersInScopes(
		ctx,
		scopes,
		strings.TrimSpace(keyword),
		offset,
		limit,
	)
}
