package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const organizeBatchSize = 500

type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
}

// NewKnowledgeFolderService creates a knowledge folder service.
func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo}
}

func (s *knowledgeFolderService) ListFolders(
	ctx context.Context, kbID string, parentID string,
) ([]*types.KnowledgeFolderNode, error) {
	return s.repo.ListChildren(ctx, kbID, parentID)
}

func (s *knowledgeFolderService) ListAllFolders(
	ctx context.Context, kbID string,
) ([]*types.KnowledgeFolder, error) {
	return s.repo.ListAll(ctx, kbID)
}

func (s *knowledgeFolderService) SearchFoldersInScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset, limit int,
) ([]*types.KnowledgeFolderSearchResult, bool, int64, error) {
	if len(scopes) == 0 {
		return nil, false, 0, nil
	}
	return s.repo.SearchFoldersInScopes(ctx, scopes, keyword, offset, limit)
}

func (s *knowledgeFolderService) GetFolder(
	ctx context.Context, kbID string, id string,
) (*types.KnowledgeFolder, error) {
	return s.repo.GetByID(ctx, kbID, id)
}

func validateKnowledgeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("folder name is required")
	}
	if strings.ContainsAny(name, "/\\｜|／") {
		return "", fmt.Errorf("folder name %q must not contain a path separator", name)
	}
	return name, nil
}

func (s *knowledgeFolderService) CreateFolder(
	ctx context.Context, kbID string, tenantID uint64, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	name, err := validateKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	depth := 1
	if parentID != types.KnowledgeFolderRootID {
		parent, err := s.repo.GetByID(ctx, kbID, parentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	if depth > types.KnowledgeFolderMaxDepth {
		return nil, fmt.Errorf("folder tree deeper than %d levels is not supported", types.KnowledgeFolderMaxDepth)
	}

	if _, err := s.repo.GetChildByName(ctx, kbID, parentID, name); err == nil {
		return nil, repository.ErrKnowledgeFolderConflict
	} else if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}

	path := name
	if parentPath != "" {
		path = parentPath + "/" + name
	}
	now := time.Now()
	folder := &types.KnowledgeFolder{
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
	if err := s.repo.Create(ctx, folder); err != nil {
		return nil, fmt.Errorf("create knowledge folder: %w", err)
	}
	return folder, nil
}

func cleanFolderPathSegments(path []string) []string {
	out := make([]string, 0, len(path))
	for _, seg := range path {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func (s *knowledgeFolderService) FindOrCreateFolderPath(
	ctx context.Context, kbID string, tenantID uint64, baseFolderID string, path []string,
) (string, error) {
	leafID, _, err := s.findOrCreateFolderPath(ctx, kbID, tenantID, baseFolderID, path)
	return leafID, err
}

// Re-fetch after a create conflict to tolerate concurrent folder creation.
func (s *knowledgeFolderService) findOrCreateFolderPath(
	ctx context.Context, kbID string, tenantID uint64, baseFolderID string, path []string,
) (string, int64, error) {
	clean := cleanFolderPathSegments(path)
	if len(clean) == 0 {
		return baseFolderID, 0, nil
	}

	parentID := baseFolderID
	parentPath := ""
	baseDepth := 0
	if baseFolderID != types.KnowledgeFolderRootID {
		base, err := s.repo.GetByID(ctx, kbID, baseFolderID)
		if err != nil {
			return "", 0, err
		}
		parentPath = base.Path
		baseDepth = base.Depth
	}
	if baseDepth+len(clean) > types.KnowledgeFolderMaxDepth {
		return "", 0, fmt.Errorf("folder tree deeper than %d levels is not supported", types.KnowledgeFolderMaxDepth)
	}

	var created int64
	for i, name := range clean {
		child, err := s.repo.GetChildByName(ctx, kbID, parentID, name)
		if err != nil {
			if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
				return "", created, err
			}
			fp := name
			if parentPath != "" {
				fp = parentPath + "/" + name
			}
			now := time.Now()
			child = &types.KnowledgeFolder{
				ID:              uuid.New().String(),
				TenantID:        tenantID,
				KnowledgeBaseID: kbID,
				ParentID:        parentID,
				Name:            name,
				Path:            fp,
				Depth:           baseDepth + i + 1,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if cerr := s.repo.Create(ctx, child); cerr != nil {
				// Lost a create race (unique violation): the sibling must now
				// exist — re-fetch it rather than failing the whole chain.
				child, err = s.repo.GetChildByName(ctx, kbID, parentID, name)
				if err != nil {
					return "", created, fmt.Errorf("create knowledge folder %q: %w", fp, cerr)
				}
			} else {
				created++
			}
		}
		parentID = child.ID
		parentPath = child.Path
	}
	return parentID, created, nil
}

func (s *knowledgeFolderService) RenameOrMoveFolder(
	ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool,
) (*types.KnowledgeFolder, error) {
	folder, err := s.repo.GetByID(ctx, kbID, id)
	if err != nil {
		return nil, err
	}

	name := folder.Name
	if strings.TrimSpace(newName) != "" {
		if name, err = validateKnowledgeFolderName(newName); err != nil {
			return nil, err
		}
	}

	targetParent := folder.ParentID
	if moveParent {
		targetParent = strings.TrimSpace(newParentID)
	}

	parentPath := ""
	depthBase := 0
	if targetParent != types.KnowledgeFolderRootID {
		if targetParent == folder.ID {
			return nil, errors.New("cannot move a folder into itself")
		}
		parent, err := s.repo.GetByID(ctx, kbID, targetParent)
		if err != nil {
			return nil, err
		}
		if parent.Path == folder.Path || strings.HasPrefix(parent.Path, folder.Path+"/") {
			return nil, errors.New("cannot move a folder into its own descendant")
		}
		parentPath = parent.Path
		depthBase = parent.Depth
	}

	if existing, err := s.repo.GetChildByName(ctx, kbID, targetParent, name); err == nil {
		if existing.ID != folder.ID {
			return nil, repository.ErrKnowledgeFolderConflict
		}
	} else if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}

	oldPath := folder.Path
	newPath := name
	if parentPath != "" {
		newPath = parentPath + "/" + name
	}
	if newPath == oldPath && targetParent == folder.ParentID {
		return folder, nil // no-op
	}

	all, err := s.repo.ListAll(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Depth cap must hold for the deepest descendant after the move.
	depthShift := (depthBase + 1) - folder.Depth
	for _, f := range all {
		inSubtree := f.ID == folder.ID || strings.HasPrefix(f.Path, oldPath+"/")
		if inSubtree && f.Depth+depthShift > types.KnowledgeFolderMaxDepth {
			return nil, fmt.Errorf("folder tree deeper than %d levels is not supported", types.KnowledgeFolderMaxDepth)
		}
	}

	now := time.Now()
	var updated *types.KnowledgeFolder
	pending := make([]*types.KnowledgeFolder, 0, len(all))

	for _, f := range all {
		switch {
		case f.ID == folder.ID:
			f.ParentID = targetParent
			f.Name = name
			f.Path = newPath
			f.Depth = depthBase + 1
		case strings.HasPrefix(f.Path, oldPath+"/"):
			f.Path = newPath + f.Path[len(oldPath):]
			f.Depth += depthShift
		default:
			continue
		}
		f.UpdatedAt = now
		pending = append(pending, f)
		if f.ID == folder.ID {
			updated = f
		}
	}

	// The whole subtree lands in one transaction: a half-written batch would
	// leave the materialized paths permanently out of sync with parent_id.
	if err := s.repo.UpdateSubtree(ctx, pending); err != nil {
		return nil, err
	}

	if updated == nil {
		updated = folder
	}
	return updated, nil
}

func (s *knowledgeFolderService) DeleteFolder(
	ctx context.Context, kbID string, id string, promote bool,
) error {
	folder, err := s.repo.GetByID(ctx, kbID, id)
	if err != nil {
		return err
	}

	if promote {
		if _, err := s.repo.MoveKnowledgeBetweenFolders(ctx, kbID, id, folder.ParentID); err != nil {
			return fmt.Errorf("promote documents of folder %q: %w", folder.Path, err)
		}
		children, err := s.repo.ListChildren(ctx, kbID, id)
		if err != nil {
			return err
		}
		for _, child := range children {
			if _, err := s.RenameOrMoveFolder(ctx, kbID, child.ID, "", folder.ParentID, true); err != nil {
				// A sibling with the same name may already live at the parent;
				// surface the conflict so the user can rename first.
				return fmt.Errorf("promote child folder %q: %w", child.Name, err)
			}
		}
	}

	return s.repo.Delete(ctx, kbID, id)
}

func (s *knowledgeFolderService) MoveKnowledgeToFolder(
	ctx context.Context, kbID string, knowledgeIDs []string, folderID string,
) (int64, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID != types.KnowledgeFolderRootID {
		if _, err := s.repo.GetByID(ctx, kbID, folderID); err != nil {
			return 0, err
		}
	}
	return s.repo.BatchUpdateKnowledgeFolder(ctx, kbID, knowledgeIDs, folderID)
}

func (s *knowledgeFolderService) OrganizeByPath(
	ctx context.Context, kbID string, tenantID uint64,
) (int64, int64, error) {
	var organized, foldersCreated int64
	seen := make(map[string]bool)
	for {
		rows, err := s.repo.ListPathedRootKnowledge(ctx, kbID, organizeBatchSize)
		if err != nil {
			return organized, foldersCreated, err
		}
		progress := false
		for _, row := range rows {
			if row == nil || seen[row.ID] {
				continue
			}
			seen[row.ID] = true

			segments := strings.Split(row.FileName, "/")
			dirs := segments[:len(segments)-1]
			leafID, created, err := s.findOrCreateFolderPath(ctx, kbID, tenantID, types.KnowledgeFolderRootID, dirs)
			if err != nil {
				logger.Warnf(ctx, "organize-by-path: resolve folders for %q failed: %v", row.FileName, err)
				continue
			}
			foldersCreated += created
			if leafID == types.KnowledgeFolderRootID {
				// Degenerate path (e.g. "/a.pdf") resolves to the root the row
				// already sits in; skip so the loop cannot spin on it.
				continue
			}
			moved, err := s.repo.BatchUpdateKnowledgeFolder(ctx, kbID, []string{row.ID}, leafID)
			if err != nil {
				logger.Warnf(ctx, "organize-by-path: file %q into folder failed: %v", row.FileName, err)
				continue
			}
			organized += moved
			progress = true
		}
		if len(rows) < organizeBatchSize || !progress {
			return organized, foldersCreated, nil
		}
	}
}

func (s *knowledgeFolderService) ExpandFolderSubtrees(
	ctx context.Context, kbID string, folderIDs []string,
) ([]string, error) {
	roots := make(map[string]bool, len(folderIDs))
	for _, id := range folderIDs {
		if id = strings.TrimSpace(id); id != "" {
			roots[id] = true
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}

	all, err := s.repo.ListAll(ctx, kbID)
	if err != nil {
		return nil, err
	}
	childrenOf := make(map[string][]string, len(all))
	live := make(map[string]bool, len(all))
	for _, f := range all {
		live[f.ID] = true
		childrenOf[f.ParentID] = append(childrenOf[f.ParentID], f.ID)
	}

	var out []string
	queue := make([]string, 0, len(roots))
	visited := make(map[string]bool, len(all))
	for id := range roots {
		if live[id] && !visited[id] {
			visited[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, id)
		for _, child := range childrenOf[id] {
			if !visited[child] {
				visited[child] = true
				queue = append(queue, child)
			}
		}
	}
	return out, nil
}

// ListKnowledgeIDsByFolderIDs preserves an empty scope as empty.
func (s *knowledgeFolderService) ListKnowledgeIDsByFolderIDs(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool,
) ([]string, error) {
	ids := folderIDs
	if recursive {
		expanded, err := s.ExpandFolderSubtrees(ctx, kbID, folderIDs)
		if err != nil {
			return nil, err
		}
		ids = expanded
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.repo.ListKnowledgeIDsInFolders(ctx, tenantID, kbID, ids)
}

// Reset document folder IDs before deleting the folder tree.
func (s *knowledgeFolderService) DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error {
	if _, err := s.repo.ResetKnowledgeFolders(ctx, kbID); err != nil {
		return err
	}
	return s.repo.DeleteByKnowledgeBase(ctx, kbID)
}
