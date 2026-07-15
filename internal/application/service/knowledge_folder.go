package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// GetFolder retrieves a single knowledge folder by id.
func (s *knowledgeService) GetFolder(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error) {
	return s.repo.GetFolderByID(ctx, kbID, id)
}

// ListChildFolders returns the direct children of parentID for a tree view.
// KnowledgeCount is recursive (the folder's whole subtree) so a parent
// reflects everything filed beneath it. A folder is shown when its subtree
// holds at least one knowledge; wholly-empty folders are shown too so the
// user can still navigate and file documents into them.
func (s *knowledgeService) ListChildFolders(
	ctx context.Context, kbID string, parentID string,
) ([]types.KnowledgeFolderNode, error) {
	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	direct, err := s.repo.CountKnowledgesByFolder(ctx, kbID)
	if err != nil {
		return nil, err
	}
	rec := recursiveKnowledgeFolderCounts(all, direct)

	out := make([]types.KnowledgeFolderNode, 0)
	for _, f := range all {
		if f.ParentID != parentID {
			continue
		}
		hasChildren := false
		for _, g := range all {
			if g.ParentID == f.ID {
				hasChildren = true
				break
			}
		}
		out = append(out, types.KnowledgeFolderNode{
			KnowledgeFolder: *f,
			KnowledgeCount:  rec[f.ID],
			HasChildren:     hasChildren,
		})
	}
	return out, nil
}

// recursiveKnowledgeFolderCounts maps each folder id to the sum of `direct`
// knowledge counts over the folder and all of its descendants, using the
// materialized path so a single pass over the (navigation-sized) folder set
// suffices.
func recursiveKnowledgeFolderCounts(all []*types.KnowledgeFolder, direct map[string]int64) map[string]int64 {
	res := make(map[string]int64, len(all))
	for _, f := range all {
		sum := direct[f.ID]
		prefix := f.Path + "/"
		for _, g := range all {
			if g.ID != f.ID && strings.HasPrefix(g.Path, prefix) {
				sum += direct[g.ID]
			}
		}
		res[f.ID] = sum
	}
	return res
}

// CreateFolder creates a new empty folder under parentID.
func (s *knowledgeService) CreateFolder(
	ctx context.Context, kbID string, tenantID uint64, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	name, err := validateFolderName(name)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	depth := 1
	if parentID != types.KnowledgeFolderRootID {
		parent, err := s.repo.GetFolderByID(ctx, kbID, parentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	if depth > types.KnowledgeFolderMaxDepth {
		return nil, fmt.Errorf("folder nesting exceeds maximum depth of %d", types.KnowledgeFolderMaxDepth)
	}

	if _, err := s.repo.GetChildFolderByName(ctx, kbID, parentID, name); err == nil {
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
	if err := s.repo.CreateFolder(ctx, folder); err != nil {
		return nil, fmt.Errorf("create knowledge folder: %w", err)
	}
	return folder, nil
}

// RenameOrMoveFolder renames and/or reparents a folder, then recomputes the
// materialized path/depth of the entire subtree. Guards against cycles
// (moving a folder into itself or one of its descendants) and sibling name
// collisions.
func (s *knowledgeService) RenameOrMoveFolder(
	ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool,
) (*types.KnowledgeFolder, error) {
	folder, err := s.repo.GetFolderByID(ctx, kbID, id)
	if err != nil {
		return nil, err
	}

	name := folder.Name
	if strings.TrimSpace(newName) != "" {
		if name, err = validateFolderName(newName); err != nil {
			return nil, err
		}
	}

	targetParent := folder.ParentID
	if moveParent {
		targetParent = newParentID
	}

	parentPath := ""
	depthBase := 0
	if targetParent != types.KnowledgeFolderRootID {
		if targetParent == folder.ID {
			return nil, errors.New("cannot move a folder into itself")
		}
		parent, err := s.repo.GetFolderByID(ctx, kbID, targetParent)
		if err != nil {
			return nil, err
		}
		if parent.Path == folder.Path || strings.HasPrefix(parent.Path, folder.Path+"/") {
			return nil, errors.New("cannot move a folder into its own descendant")
		}
		if parent.Depth+1 > types.KnowledgeFolderMaxDepth {
			return nil, fmt.Errorf("folder nesting exceeds maximum depth of %d", types.KnowledgeFolderMaxDepth)
		}
		parentPath = parent.Path
		depthBase = parent.Depth
	}

	if existing, err := s.repo.GetChildFolderByName(ctx, kbID, targetParent, name); err == nil {
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

	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var updated *types.KnowledgeFolder
	for _, f := range all {
		switch {
		case f.ID == folder.ID:
			f.ParentID = targetParent
			f.Name = name
			f.Path = newPath
			f.Depth = depthBase + 1
		case strings.HasPrefix(f.Path, oldPath+"/"):
			f.Path = newPath + f.Path[len(oldPath):]
			f.Depth = len(strings.Split(f.Path, "/"))
		default:
			continue
		}
		f.UpdatedAt = now
		if err := s.repo.UpdateFolder(ctx, f); err != nil {
			return nil, err
		}
		if f.ID == folder.ID {
			updated = f
		}
	}
	if updated == nil {
		updated = folder
	}
	return updated, nil
}

// ListAllFoldersFlat returns every folder in the knowledge base (not just one
// level) enriched with a recursively-computed knowledge count and a
// has-children flag. It powers flat pickers such as the chat @mention folder
// list, where we want the whole tree in a single request rather than one
// round-trip per directory.
func (s *knowledgeService) ListAllFoldersFlat(ctx context.Context, kbID string) ([]types.KnowledgeFolderNode, error) {
	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	direct, err := s.repo.CountKnowledgesByFolder(ctx, kbID)
	if err != nil {
		return nil, err
	}
	rec := recursiveKnowledgeFolderCounts(all, direct)

	out := make([]types.KnowledgeFolderNode, 0, len(all))
	for _, f := range all {
		hasChildren := false
		for _, g := range all {
			if g.ParentID == f.ID {
				hasChildren = true
				break
			}
		}
		out = append(out, types.KnowledgeFolderNode{
			KnowledgeFolder: *f,
			KnowledgeCount:  rec[f.ID],
			HasChildren:     hasChildren,
		})
	}
	return out, nil
}

// ExpandFolderToKnowledgeIDs resolves a @mentioned folder to the concrete
// retrieval scope it represents: the owning knowledge base, that KB's tenant,
// and the IDs of every knowledge filed directly in the folder or anywhere in
// its subtree. The QA pipeline turns this into a SearchTargetTypeKnowledge
// target so "ask about this folder" only retrieves that folder's contents.
func (s *knowledgeService) ExpandFolderToKnowledgeIDs(
	ctx context.Context, folderID string,
) (kbID string, tenantID uint64, knowledgeIDs []string, err error) {
	folder, ferr := s.repo.GetFolderByIDGlobal(ctx, folderID)
	if ferr != nil {
		return "", 0, nil, ferr
	}
	kbID = folder.KnowledgeBaseID
	tenantID = folder.TenantID

	all, aerr := s.repo.ListAllFolders(ctx, kbID)
	if aerr != nil {
		return "", 0, nil, aerr
	}
	ids := make([]string, 0, len(all))
	prefix := folder.Path + "/"
	for _, f := range all {
		if f.ID == folderID || strings.HasPrefix(f.Path, prefix) {
			ids = append(ids, f.ID)
		}
	}

	knowledges, kerr := s.repo.ListKnowledgesByFolderIDs(ctx, kbID, ids)
	if kerr != nil {
		return "", 0, nil, kerr
	}
	out := make([]string, 0, len(knowledges))
	for _, k := range knowledges {
		out = append(out, k.ID)
	}
	return kbID, tenantID, out, nil
}

// DeleteFolder removes a folder that has no knowledges and no child folders.
// The UI must relocate contents first; this keeps deletion non-destructive.
func (s *knowledgeService) DeleteFolder(ctx context.Context, kbID string, id string) error {
	if _, err := s.repo.GetFolderByID(ctx, kbID, id); err != nil {
		return err
	}
	children, err := s.repo.ListChildFolders(ctx, kbID, id)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return errors.New("folder is not empty: it still has sub-folders")
	}
	knowledges, err := s.repo.ListKnowledgesByFolderIDs(ctx, kbID, []string{id})
	if err != nil {
		return err
	}
	if len(knowledges) > 0 {
		return errors.New("folder is not empty: it still contains knowledges")
	}
	return s.repo.DeleteFolder(ctx, kbID, id)
}

// SetKnowledgeFolder moves a knowledge into a folder (empty folderID = root).
func (s *knowledgeService) SetKnowledgeFolder(
	ctx context.Context, kbID string, knowledgeID string, folderID string,
) (*types.Knowledge, error) {
	if folderID != types.KnowledgeFolderRootID {
		if _, err := s.repo.GetFolderByID(ctx, kbID, folderID); err != nil {
			return nil, err
		}
	}
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	k, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	if k.KnowledgeBaseID != kbID {
		return nil, repository.ErrKnowledgeNotFound
	}
	if err := s.repo.UpdateKnowledgeFolder(ctx, knowledgeID, folderID); err != nil {
		return nil, fmt.Errorf("set knowledge folder: %w", err)
	}
	fid := folderID
	k.FolderID = &fid
	return k, nil
}
