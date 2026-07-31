package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// knowledgeFolderService implements the knowledge folder tree.
type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
}

// NewKnowledgeFolderService creates a new knowledge folder service.
func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo}
}

// validateKnowledgeFolderName normalises and checks a folder name.
//
// The separator characters are rejected even though Path stores ids rather
// than names: names still surface in breadcrumbs and export paths, and letting
// one through would produce a display path that reads like two folders.
func validateKnowledgeFolderName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("folder name cannot be empty")
	}
	if len([]rune(trimmed)) > types.KnowledgeFolderMaxNameLength {
		return "", fmt.Errorf("folder name cannot exceed %d characters", types.KnowledgeFolderMaxNameLength)
	}
	if strings.ContainsAny(trimmed, `/\｜|／`) {
		return "", errors.New("folder name cannot contain path separators")
	}
	// "." and ".." would be ambiguous in any exported filesystem path.
	if trimmed == "." || trimmed == ".." {
		return "", errors.New("folder name is reserved")
	}
	return trimmed, nil
}

// CreateFolder creates an empty folder under parentID.
func (s *knowledgeFolderService) CreateFolder(
	ctx context.Context, tenantID uint64, kbID string, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	name, err := validateKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	depth := 1
	if parentID != types.KnowledgeFolderRootID {
		parent, err := s.repo.GetFolderByID(ctx, tenantID, kbID, parentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
		if depth > types.KnowledgeFolderMaxDepth {
			return nil, repository.ErrKnowledgeFolderTooDeep
		}
	}

	// Pre-check the sibling name so the common case returns a clean conflict
	// rather than a raw unique-constraint violation. The database index remains
	// the actual guarantee under concurrency.
	if _, err := s.repo.GetChildFolderByName(ctx, tenantID, kbID, parentID, name); err == nil {
		return nil, repository.ErrKnowledgeFolderConflict
	} else if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}

	folder := &types.KnowledgeFolder{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Depth:           depth,
	}
	folder.Path = types.BuildKnowledgeFolderPath(parentPath, folder.ID)

	if err := s.repo.CreateFolder(ctx, folder); err != nil {
		return nil, fmt.Errorf("create knowledge folder: %w", err)
	}
	logger.Infof(ctx, "Created knowledge folder %s (kb=%s, depth=%d)", folder.ID, kbID, folder.Depth)
	return folder, nil
}

// GetFolder loads a single folder.
func (s *knowledgeFolderService) GetFolder(
	ctx context.Context, tenantID uint64, kbID string, id string,
) (*types.KnowledgeFolder, error) {
	return s.repo.GetFolderByID(ctx, tenantID, kbID, id)
}

// ListFolders assembles the folder view for a knowledge base.
//
// When recursive is true the whole tree is returned in one response; otherwise
// only the direct children of parentID. Either way both a direct and a subtree
// document count are attached, because a collapsed row needs the subtree total
// while an expanded one needs the direct figure.
func (s *knowledgeFolderService) ListFolders(
	ctx context.Context, tenantID uint64, kbID string, parentID string, recursive bool,
) (*types.KnowledgeFolderListResponse, error) {
	all, err := s.repo.ListAllFolders(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	counts, err := s.repo.CountDocumentsByFolder(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}

	totals := aggregateSubtreeCounts(all, counts)
	childCount := make(map[string]int, len(all))
	byID := make(map[string]*types.KnowledgeFolder, len(all))
	for _, folder := range all {
		byID[folder.ID] = folder
		childCount[folder.ParentID]++
	}

	selected := all
	if !recursive {
		selected = make([]*types.KnowledgeFolder, 0, len(all))
		for _, folder := range all {
			if folder.ParentID == parentID {
				selected = append(selected, folder)
			}
		}
	}

	nodes := make([]types.KnowledgeFolderNode, 0, len(selected))
	for _, folder := range selected {
		nodes = append(nodes, types.KnowledgeFolderNode{
			KnowledgeFolder:    *folder,
			DocumentCount:      counts[folder.ID],
			TotalDocumentCount: totals[folder.ID],
			HasChildren:        childCount[folder.ID] > 0,
			NamePath:           buildFolderNamePath(folder, byID),
		})
	}

	return &types.KnowledgeFolderListResponse{
		ParentID:          parentID,
		Folders:           nodes,
		RootDocumentCount: counts[types.KnowledgeFolderRootID],
	}, nil
}

// aggregateSubtreeCounts rolls direct per-folder counts up into subtree totals.
//
// Walking the folders deepest-first and adding each total into its parent
// visits every node once. The obvious alternative — for each folder, scan all
// others for a matching path prefix — is quadratic, which starts to hurt on the
// large trees this feature exists to support.
func aggregateSubtreeCounts(
	folders []*types.KnowledgeFolder, direct map[string]int64,
) map[string]int64 {
	totals := make(map[string]int64, len(folders))
	for _, folder := range folders {
		totals[folder.ID] = direct[folder.ID]
	}
	ordered := make([]*types.KnowledgeFolder, len(folders))
	copy(ordered, folders)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Depth > ordered[j].Depth
	})
	for _, folder := range ordered {
		if folder.ParentID == types.KnowledgeFolderRootID {
			continue
		}
		if _, ok := totals[folder.ParentID]; ok {
			totals[folder.ParentID] += totals[folder.ID]
		}
	}
	return totals
}

// buildFolderNamePath resolves a folder's ancestor chain into display names. It
// tolerates a missing ancestor (possible if a row was removed concurrently)
// by stopping rather than returning a misleading partial-looking path.
func buildFolderNamePath(folder *types.KnowledgeFolder, byID map[string]*types.KnowledgeFolder) []string {
	ids := types.KnowledgeFolderPathIDs(folder.Path)
	if len(ids) == 0 {
		return []string{folder.Name}
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		ancestor, ok := byID[id]
		if !ok {
			continue
		}
		names = append(names, ancestor.Name)
	}
	return names
}

// RenameOrMoveFolder renames a folder and/or reparents it.
//
// Moving rewrites the materialized path of the folder and every descendant.
// Because paths are id chains, a pure rename touches exactly one row.
func (s *knowledgeFolderService) RenameOrMoveFolder(
	ctx context.Context, tenantID uint64, kbID string, id string,
	newName string, newParentID string, moveParent bool,
) (*types.KnowledgeFolder, error) {
	folder, err := s.repo.GetFolderByID(ctx, tenantID, kbID, id)
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
		targetParent = newParentID
	}

	parentPath := ""
	parentDepth := 0
	if targetParent != types.KnowledgeFolderRootID {
		if targetParent == folder.ID {
			return nil, errors.New("cannot move a folder into itself")
		}
		parent, err := s.repo.GetFolderByID(ctx, tenantID, kbID, targetParent)
		if err != nil {
			return nil, err
		}
		// Cycle guard: the destination must not live inside the subtree being
		// moved. Prefix-matching the id path catches this at any depth, and
		// without it the subtree would be detached from the root entirely.
		if types.IsDescendantOfKnowledgeFolder(parent.Path, folder.Path) {
			return nil, errors.New("cannot move a folder into its own descendant")
		}
		parentPath = parent.Path
		parentDepth = parent.Depth
	}

	// Sibling-name uniqueness, ignoring the folder itself so a no-op rename works.
	if existing, err := s.repo.GetChildFolderByName(ctx, tenantID, kbID, targetParent, name); err == nil {
		if existing.ID != folder.ID {
			return nil, repository.ErrKnowledgeFolderConflict
		}
	} else if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}

	renamed := name != folder.Name
	reparented := targetParent != folder.ParentID
	if !renamed && !reparented {
		return folder, nil
	}

	if !reparented {
		folder.Name = name
		if err := s.repo.UpdateFolder(ctx, folder); err != nil {
			return nil, fmt.Errorf("rename knowledge folder: %w", err)
		}
		return folder, nil
	}

	subtree, err := s.repo.ListSubtreeFolders(ctx, tenantID, kbID, folder.Path)
	if err != nil {
		return nil, err
	}

	oldPath := folder.Path
	newPath := types.BuildKnowledgeFolderPath(parentPath, folder.ID)
	depthShift := (parentDepth + 1) - folder.Depth

	// Reject the move if it would push any descendant past the nesting cap,
	// checked before writing anything so the tree is never left half-moved.
	for _, node := range subtree {
		if node.Depth+depthShift > types.KnowledgeFolderMaxDepth {
			return nil, repository.ErrKnowledgeFolderTooDeep
		}
	}

	updates := make([]*types.KnowledgeFolder, 0, len(subtree))
	for _, node := range subtree {
		if node.ID == folder.ID {
			node.ParentID = targetParent
			node.Name = name
			node.Path = newPath
			node.Depth = parentDepth + 1
		} else {
			node.Path = newPath + strings.TrimPrefix(node.Path, oldPath)
			node.Depth += depthShift
		}
		updates = append(updates, node)
	}

	if err := s.repo.UpdateFoldersTx(ctx, updates); err != nil {
		return nil, fmt.Errorf("move knowledge folder: %w", err)
	}

	folder.ParentID = targetParent
	folder.Name = name
	folder.Path = newPath
	folder.Depth = parentDepth + 1
	logger.Infof(ctx, "Moved knowledge folder %s to parent %s (%d rows)", folder.ID, targetParent, len(updates))
	return folder, nil
}

// DeleteFolder removes a folder.
//
// The default strategy refuses to delete anything that still holds documents or
// child folders, so a mis-click cannot take a subtree's contents with it. The
// "reparent" strategy is the explicit opt-in: it deletes the subtree and lifts
// every document it held up to the deleted folder's parent. Documents are never
// destroyed by a folder operation either way.
func (s *knowledgeFolderService) DeleteFolder(
	ctx context.Context, tenantID uint64, kbID string, id string, strategy string,
) error {
	folder, err := s.repo.GetFolderByID(ctx, tenantID, kbID, id)
	if err != nil {
		return err
	}

	if strategy != types.KnowledgeFolderDeleteReparent {
		return s.repo.DeleteFolder(ctx, tenantID, kbID, id)
	}

	subtree, err := s.repo.ListSubtreeFolders(ctx, tenantID, kbID, folder.Path)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(subtree))
	for _, node := range subtree {
		ids = append(ids, node.ID)
	}
	if len(ids) == 0 {
		ids = []string{folder.ID}
	}
	if err := s.repo.DeleteFolderTree(ctx, tenantID, kbID, ids, folder.ParentID); err != nil {
		return fmt.Errorf("delete knowledge folder tree: %w", err)
	}
	logger.Infof(ctx, "Deleted knowledge folder subtree %s (%d folders)", id, len(ids))
	return nil
}

// ResolveFolderIDs expands a set of folder ids into the concrete id set a
// listing or retrieval scope should match.
//
// With recursive set, each requested folder contributes its whole subtree. The
// root sentinel "" is preserved as-is: it is a real bucket (documents not filed
// anywhere) and has no folder row to expand. Recursing from the root pulls in
// every folder of the knowledge base, which is what "ask the whole base"
// should mean.
func (s *knowledgeFolderService) ResolveFolderIDs(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool,
) ([]string, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}

	resolved := make(map[string]struct{}, len(folderIDs))
	needsAll := false
	for _, id := range folderIDs {
		resolved[id] = struct{}{}
		if id == types.KnowledgeFolderRootID && recursive {
			needsAll = true
		}
	}

	if !recursive {
		return sortedFolderIDs(resolved), nil
	}

	if needsAll {
		all, err := s.repo.ListAllFolders(ctx, tenantID, kbID)
		if err != nil {
			return nil, err
		}
		for _, folder := range all {
			resolved[folder.ID] = struct{}{}
		}
		return sortedFolderIDs(resolved), nil
	}

	for _, id := range folderIDs {
		if id == types.KnowledgeFolderRootID {
			continue
		}
		folder, err := s.repo.GetFolderByID(ctx, tenantID, kbID, id)
		if err != nil {
			// A stale id in a saved scope should narrow the result, not fail the
			// whole request.
			if errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
				logger.Warnf(ctx, "Skipping unknown folder %s in scope for kb %s", id, kbID)
				delete(resolved, id)
				continue
			}
			return nil, err
		}
		subtree, err := s.repo.ListSubtreeFolders(ctx, tenantID, kbID, folder.Path)
		if err != nil {
			return nil, err
		}
		for _, node := range subtree {
			resolved[node.ID] = struct{}{}
		}
	}
	return sortedFolderIDs(resolved), nil
}

// ListKnowledgeIDsByFolders resolves a folder scope into the document ids it
// contains, which is the form the retrieval pipeline already filters on.
func (s *knowledgeFolderService) ListKnowledgeIDsByFolders(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool,
) ([]string, error) {
	resolved, err := s.ResolveFolderIDs(ctx, tenantID, kbID, folderIDs, recursive)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return s.repo.ListKnowledgeIDsByFolderIDs(ctx, tenantID, kbID, resolved)
}

// MoveKnowledgeToFolder files documents into a target folder ("" = root).
func (s *knowledgeFolderService) MoveKnowledgeToFolder(
	ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, targetFolderID string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	if targetFolderID != types.KnowledgeFolderRootID {
		if _, err := s.repo.GetFolderByID(ctx, tenantID, kbID, targetFolderID); err != nil {
			return 0, err
		}
	}
	moved, err := s.repo.MoveKnowledgeToFolder(ctx, tenantID, kbID, knowledgeIDs, targetFolderID)
	if err != nil {
		return 0, fmt.Errorf("move documents to folder: %w", err)
	}
	logger.Infof(ctx, "Moved %d documents to folder %q in kb %s", moved, targetFolderID, kbID)
	return moved, nil
}

// FindOrCreateFolderPath walks a chain of folder names, creating the missing
// levels, and returns the id of the deepest one. Import flows use it to mirror
// a source directory layout.
func (s *knowledgeFolderService) FindOrCreateFolderPath(
	ctx context.Context, tenantID uint64, kbID string, names []string,
) (string, error) {
	parentID := types.KnowledgeFolderRootID
	for _, raw := range names {
		name, err := validateKnowledgeFolderName(raw)
		if err != nil {
			// Skip empty segments so a trailing slash in a source path is
			// harmless rather than fatal.
			if strings.TrimSpace(raw) == "" {
				continue
			}
			return "", err
		}
		child, err := s.repo.GetChildFolderByName(ctx, tenantID, kbID, parentID, name)
		if err == nil {
			parentID = child.ID
			continue
		}
		if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
			return "", err
		}
		created, err := s.CreateFolder(ctx, tenantID, kbID, parentID, name)
		if err != nil {
			// Losing a create race means a sibling with this name now exists;
			// adopt it instead of failing the whole import.
			if errors.Is(err, repository.ErrKnowledgeFolderConflict) {
				existing, lookupErr := s.repo.GetChildFolderByName(ctx, tenantID, kbID, parentID, name)
				if lookupErr == nil {
					parentID = existing.ID
					continue
				}
			}
			return "", err
		}
		parentID = created.ID
	}
	return parentID, nil
}

// sortedFolderIDs returns map keys in a stable order so generated SQL and logs are
// deterministic.
func sortedFolderIDs(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
