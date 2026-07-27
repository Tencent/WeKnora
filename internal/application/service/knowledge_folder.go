package service

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// KnowledgeFolderService is kept as a package alias for callers that construct the concrete service.
type KnowledgeFolderService = interfaces.KnowledgeFolderService

type knowledgeFolderService struct {
	folders   interfaces.KnowledgeFolderRepository
	knowledge interfaces.KnowledgeRepository
	kb        interfaces.KnowledgeBaseRepository
}

func NewKnowledgeFolderService(
	folders interfaces.KnowledgeFolderRepository,
	knowledge interfaces.KnowledgeRepository,
	kb interfaces.KnowledgeBaseRepository,
) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{folders: folders, knowledge: knowledge, kb: kb}
}

func (s *knowledgeFolderService) scope(ctx context.Context, kbID string) (uint64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || strings.TrimSpace(kbID) == "" {
		return 0, types.ErrInvalidArgument
	}
	kb, err := s.kb.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return 0, err
	}
	if kb == nil || kb.ID != kbID || kb.TenantID != tenantID {
		return 0, repository.ErrKnowledgeFolderNotFound
	}
	return tenantID, nil
}

func normalizeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", types.ErrInvalidArgument
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", types.ErrInvalidArgument
		}
	}
	return name, nil
}

func folderPath(parent *types.KnowledgeFolder, id string) string {
	if parent == nil {
		return "/" + id
	}
	return parent.Path + "/" + id
}

func (s *knowledgeFolderService) parent(
	ctx context.Context, repo interfaces.KnowledgeFolderRepository, tenantID uint64, kbID, parentID string, lock bool,
) (*types.KnowledgeFolder, int, error) {
	if parentID == types.FolderRootID {
		return nil, 1, nil
	}
	var parent *types.KnowledgeFolder
	var err error
	if lock {
		parent, err = repo.GetByIDForUpdate(ctx, tenantID, kbID, parentID)
	} else {
		parent, err = repo.GetByID(ctx, tenantID, kbID, parentID)
	}
	if err != nil {
		return nil, 0, err
	}
	return parent, parent.Depth + 1, nil
}

func (s *knowledgeFolderService) CreateFolder(
	ctx context.Context, kbID string, req *types.CreateFolderRequest,
) (*types.KnowledgeFolder, error) {
	if req == nil {
		return nil, types.ErrInvalidArgument
	}
	name, err := normalizeFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	var created *types.KnowledgeFolder
	err = s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		parent, depth, err := s.parent(ctx, repo, tenantID, kbID, req.ParentID, true)
		if err != nil {
			return err
		}
		if depth > types.MaxFolderDepth {
			return types.ErrInvalidArgument
		}
		exists, err := repo.CheckSiblingName(ctx, tenantID, kbID, req.ParentID, name, "")
		if err != nil {
			return err
		}
		if exists {
			return types.ErrFolderAlreadyExists
		}
		folder := &types.KnowledgeFolder{
			TenantID: tenantID, KnowledgeBaseID: kbID, ParentID: req.ParentID, Name: name, Depth: depth,
		}
		if err := folder.BeforeCreate(nil); err != nil {
			return err
		}
		folder.Path = folderPath(parent, folder.ID)
		folder, inserted, err := repo.CreateIfAbsent(ctx, folder)
		if err != nil {
			return err
		}
		if !inserted {
			return types.ErrFolderAlreadyExists
		}
		created = folder
		return nil
	})
	return created, err
}

const maxRelativeFolderPathBytes = types.MaxFolderDepth*255 + (types.MaxFolderDepth - 1)

func normalizeRelativeFolderPath(path string) (string, []string, error) {
	if path == "" {
		return "", nil, nil
	}
	driveAbsolute := len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') ||
		(path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && path[2] == '/'
	if strings.HasPrefix(path, "/") || driveAbsolute || strings.Contains(path, `\`) ||
		len(path) > maxRelativeFolderPathBytes {
		return "", nil, types.ErrInvalidArgument
	}
	segments := strings.Split(path, "/")
	if len(segments) > types.MaxFolderDepth {
		return "", nil, types.ErrInvalidArgument
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > 255 {
			return "", nil, types.ErrInvalidArgument
		}
		for _, r := range segment {
			if unicode.IsControl(r) {
				return "", nil, types.ErrInvalidArgument
			}
		}
	}
	return path, segments, nil
}

type resolvedFolderPathInput struct {
	original   string
	normalized string
	segments   []string
}

func (s *knowledgeFolderService) ResolveOrCreatePaths(
	ctx context.Context, kbID string, req *types.ResolveFolderPathsRequest,
) (*types.ResolveFolderPathsResponse, error) {
	if req == nil || len(req.Paths) == 0 || len(req.Paths) > types.MaxResolveFolderPaths {
		return nil, types.ErrInvalidArgument
	}

	inputs := make([]resolvedFolderPathInput, 0, len(req.Paths))
	unique := make(map[string][]string, len(req.Paths))
	for _, original := range req.Paths {
		normalized, segments, err := normalizeRelativeFolderPath(original)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, resolvedFolderPathInput{
			original: original, normalized: normalized, segments: segments,
		})
		if _, exists := unique[normalized]; !exists {
			unique[normalized] = segments
		}
	}

	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		leftDepth := len(unique[paths[i]])
		rightDepth := len(unique[paths[j]])
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return paths[i] < paths[j]
	})

	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	currentFolderID := strings.TrimSpace(req.CurrentFolderID)
	if currentFolderID == types.FolderRootFilter {
		currentFolderID = types.FolderRootID
	}

	folderIDs := make(map[string]string, len(unique)+1)
	err = s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		current, nextDepth, err := s.parent(ctx, repo, tenantID, kbID, currentFolderID, true)
		if err != nil {
			return err
		}
		for _, segments := range unique {
			if len(segments) > 0 && nextDepth+len(segments)-1 > types.MaxFolderDepth {
				return types.ErrInvalidArgument
			}
		}
		folderIDs[""] = currentFolderID
		foldersByPath := map[string]*types.KnowledgeFolder{"": current}

		for _, relativePath := range paths {
			segments := unique[relativePath]
			if len(segments) == 0 {
				continue
			}

			parentPath := ""
			for index, name := range segments {
				path := strings.Join(segments[:index+1], "/")
				if _, exists := folderIDs[path]; exists {
					parentPath = path
					continue
				}
				parent := foldersByPath[parentPath]
				folder := &types.KnowledgeFolder{
					TenantID: tenantID, KnowledgeBaseID: kbID, ParentID: folderIDs[parentPath],
					Name: name, Depth: nextDepth + index,
				}
				if err := folder.BeforeCreate(nil); err != nil {
					return err
				}
				folder.Path = folderPath(parent, folder.ID)
				resolved, _, err := repo.CreateIfAbsent(ctx, folder)
				if err != nil {
					return err
				}
				folderIDs[path] = resolved.ID
				foldersByPath[path] = resolved
				parentPath = path
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	response := &types.ResolveFolderPathsResponse{Paths: make([]types.ResolvedFolderPath, 0, len(inputs))}
	for _, input := range inputs {
		response.Paths = append(response.Paths, types.ResolvedFolderPath{
			RelativePath: input.original,
			FolderID:     folderIDs[input.normalized],
		})
	}
	return response, nil
}

func (s *knowledgeFolderService) GetFolder(
	ctx context.Context, kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	if folderID == types.FolderRootID {
		return nil, types.ErrInvalidArgument
	}
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	return s.folders.GetByID(ctx, tenantID, kbID, folderID)
}
func (s *knowledgeFolderService) ResolveFolderOwners(
	ctx context.Context, folderIDs []string,
) (map[string]string, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, types.ErrInvalidArgument
	}
	// Filter out the root sentinel - it has no owning KB (it IS the KB).
	realIDs := make([]string, 0, len(folderIDs))
	for _, id := range folderIDs {
		if id == "" || id == types.FolderRootFilter {
			continue
		}
		realIDs = append(realIDs, id)
	}
	owners := make(map[string]string, len(realIDs))
	if len(realIDs) == 0 {
		return owners, nil
	}
	folders, err := s.folders.GetByIDsForTenant(ctx, tenantID, realIDs)
	if err != nil {
		return nil, err
	}
	for _, f := range folders {
		if f != nil {
			owners[f.ID] = f.KnowledgeBaseID
		}
	}
	return owners, nil
}

func (s *knowledgeFolderService) ListByParent(
	ctx context.Context, kbID, parentID string,
) ([]*types.KnowledgeFolder, error) {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if parentID != types.FolderRootID {
		if _, err := s.folders.GetByID(ctx, tenantID, kbID, parentID); err != nil {
			return nil, err
		}
	}
	return s.folders.ListByParent(ctx, tenantID, kbID, parentID)
}

func (s *knowledgeFolderService) GetTree(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error) {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	folders, err := s.folders.ListAll(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	counts, err := s.folders.CountKnowledgeByFolder(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		folder.Children = nil
		folder.KnowledgeCount = counts[folder.ID]
		byID[folder.ID] = folder
	}
	roots := make([]*types.KnowledgeFolder, 0)
	for _, folder := range folders {
		if parent := byID[folder.ParentID]; parent != nil {
			parent.Children = append(parent.Children, folder)
		} else if folder.ParentID == types.FolderRootID {
			roots = append(roots, folder)
		}
	}
	var aggregate func(*types.KnowledgeFolder) int64
	aggregate = func(folder *types.KnowledgeFolder) int64 {
		total := folder.KnowledgeCount
		for _, child := range folder.Children {
			total += aggregate(child)
		}
		folder.KnowledgeCount = total
		return total
	}
	for _, root := range roots {
		aggregate(root)
	}
	return roots, nil
}

func (s *knowledgeFolderService) UpdateFolder(
	ctx context.Context, kbID, folderID string, req *types.UpdateFolderRequest,
) (*types.KnowledgeFolder, error) {
	if req == nil || folderID == types.FolderRootID {
		return nil, types.ErrInvalidArgument
	}
	name, err := normalizeFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	var renamed *types.KnowledgeFolder
	err = s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		folder, err := repo.GetByIDForUpdate(ctx, tenantID, kbID, folderID)
		if err != nil {
			return err
		}
		if name == folder.Name {
			renamed = folder
			return nil
		}
		exists, err := repo.CheckSiblingName(ctx, tenantID, kbID, folder.ParentID, name, folder.ID)
		if err != nil {
			return err
		}
		if exists {
			return types.ErrFolderAlreadyExists
		}
		if err := repo.UpdateName(ctx, tenantID, kbID, folder.ID, name); err != nil {
			return err
		}
		folder.Name = name
		renamed = folder
		return nil
	})
	return renamed, err
}

func (s *knowledgeFolderService) MoveFolder(
	ctx context.Context, kbID, folderID string, req *types.MoveFolderRequest,
) (*types.KnowledgeFolder, error) {
	if req == nil || folderID == types.FolderRootID || folderID == req.ParentID {
		return nil, types.ErrInvalidArgument
	}
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	var moved *types.KnowledgeFolder
	err = s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		folder, err := repo.GetByIDForUpdate(ctx, tenantID, kbID, folderID)
		if err != nil {
			return err
		}
		if req.ParentID == folder.ParentID {
			moved = folder
			return nil
		}
		parent, newDepth, err := s.parent(ctx, repo, tenantID, kbID, req.ParentID, true)
		if err != nil {
			return err
		}
		all, err := repo.ListAllForUpdate(ctx, tenantID, kbID)
		if err != nil {
			return err
		}
		maxDepth := folder.Depth
		for _, candidate := range all {
			if candidate.ID == req.ParentID && strings.HasPrefix(candidate.Path+"/", folder.Path+"/") {
				return types.ErrInvalidArgument
			}
			if candidate.ID == folder.ID || strings.HasPrefix(candidate.Path, folder.Path+"/") {
				if candidate.Depth > maxDepth {
					maxDepth = candidate.Depth
				}
			}
		}
		depthDelta := newDepth - folder.Depth
		if maxDepth+depthDelta > types.MaxFolderDepth {
			return types.ErrInvalidArgument
		}
		exists, err := repo.CheckSiblingName(ctx, tenantID, kbID, req.ParentID, folder.Name, folder.ID)
		if err != nil {
			return err
		}
		if exists {
			return types.ErrFolderAlreadyExists
		}
		oldPath := folder.Path
		newPath := folderPath(parent, folder.ID)
		folder.ParentID, folder.Path, folder.Depth = req.ParentID, newPath, newDepth
		if err := repo.MoveSubtree(ctx, folder, oldPath, newPath, depthDelta); err != nil {
			return err
		}
		moved = folder
		return nil
	})
	return moved, err
}

func (s *knowledgeFolderService) GetBreadcrumb(
	ctx context.Context, kbID, folderID string,
) ([]*types.KnowledgeFolder, error) {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if folderID == types.FolderRootID {
		return []*types.KnowledgeFolder{}, nil
	}
	folders, err := s.folders.ListAll(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		byID[folder.ID] = folder
	}
	current := byID[folderID]
	if current == nil {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	breadcrumb := make([]*types.KnowledgeFolder, 0, current.Depth)
	for current != nil {
		breadcrumb = append(breadcrumb, current)
		current = byID[current.ParentID]
	}
	for left, right := 0, len(breadcrumb)-1; left < right; left, right = left+1, right-1 {
		breadcrumb[left], breadcrumb[right] = breadcrumb[right], breadcrumb[left]
	}
	return breadcrumb, nil
}

func (s *knowledgeFolderService) ResolveKnowledgeScope(
	ctx context.Context, kbID string, folderIDs []string,
) (*types.FolderKnowledgeScope, error) {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	ids, fullKB, err := s.knowledge.ListIDsByFolderIDs(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return nil, err
	}
	return &types.FolderKnowledgeScope{KnowledgeIDs: ids, FullKnowledgeBase: fullKB}, nil
}

func validateTargetFolder(
	ctx context.Context, repo interfaces.KnowledgeFolderRepository, tenantID uint64, kbID, folderID string,
) error {
	if folderID == types.FolderRootID {
		return nil
	}
	_, err := repo.GetByIDForUpdate(ctx, tenantID, kbID, folderID)
	return err
}

func (s *knowledgeFolderService) CreateKnowledgeInFolder(
	ctx context.Context, knowledge *types.Knowledge, folderID string,
) error {
	if knowledge == nil || knowledge.ID == "" || knowledge.KnowledgeBaseID == "" {
		return types.ErrInvalidArgument
	}
	tenantID, err := s.scope(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if knowledge.TenantID != tenantID {
		return repository.ErrKnowledgeFolderNotFound
	}
	return s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, knowledge.KnowledgeBaseID); err != nil {
			return err
		}
		if err := validateTargetFolder(ctx, repo, tenantID, knowledge.KnowledgeBaseID, folderID); err != nil {
			return err
		}
		knowledge.FolderID = folderID
		return repo.CreateKnowledge(ctx, knowledge)
	})
}

func (s *knowledgeFolderService) MoveKnowledgeToFolder(
	ctx context.Context, knowledgeID, folderID string,
) error {
	return s.MoveKnowledgeBatchToFolder(ctx, []string{knowledgeID}, folderID)
}

func (s *knowledgeFolderService) MoveKnowledgeBatchToFolder(
	ctx context.Context, knowledgeIDs []string, folderID string,
) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || len(knowledgeIDs) == 0 {
		return types.ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(knowledgeIDs))
	ids := make([]string, 0, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return types.ErrInvalidArgument
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}

	// Resolve the KB only to choose the structural lock. All authoritative
	// knowledge/folder validation is repeated after that lock is held.
	preflight, err := s.knowledge.GetKnowledgeBatch(ctx, tenantID, ids)
	if err != nil {
		return err
	}
	if len(preflight) != len(ids) {
		return repository.ErrKnowledgeNotFound
	}
	kbID := preflight[0].KnowledgeBaseID
	for _, knowledge := range preflight {
		if knowledge.KnowledgeBaseID != kbID {
			return repository.ErrKnowledgeFolderNotFound
		}
	}

	return s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		knowledges, err := repo.GetKnowledgeBatchForUpdate(ctx, tenantID, ids)
		if err != nil {
			return err
		}
		if len(knowledges) != len(ids) {
			return repository.ErrKnowledgeNotFound
		}
		for _, knowledge := range knowledges {
			if knowledge.KnowledgeBaseID != kbID {
				return repository.ErrKnowledgeFolderNotFound
			}
		}
		if err := validateTargetFolder(ctx, repo, tenantID, kbID, folderID); err != nil {
			return err
		}
		toMove := make([]string, 0, len(knowledges))
		for _, knowledge := range knowledges {
			if knowledge.FolderID != folderID {
				toMove = append(toMove, knowledge.ID)
			}
		}
		return repo.MoveKnowledgeToFolder(ctx, tenantID, kbID, toMove, folderID)
	})
}

func (s *knowledgeFolderService) DeleteEmptySubtrees(
	ctx context.Context, kbID string, folderIDs []string,
) error {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return err
	}
	rootSelected := false
	for _, raw := range folderIDs {
		if strings.TrimSpace(raw) == types.FolderRootID {
			rootSelected = true
			break
		}
	}
	folderIDs = normalizeRequiredIDs(folderIDs)
	if !rootSelected && len(folderIDs) == 0 {
		return nil
	}
	return s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		all, err := repo.ListAllForUpdate(ctx, tenantID, kbID)
		if err != nil {
			return err
		}
		if rootSelected {
			folderIDs = folderIDs[:0]
			for _, folder := range all {
				folderIDs = append(folderIDs, folder.ID)
			}
		}

		toDelete := folderIDs
		if !rootSelected {
			toDelete, err = repo.GetDescendantIDs(ctx, tenantID, kbID, folderIDs)
			if err != nil {
				return err
			}
		}
		counts, err := repo.CountKnowledgeByFolder(ctx, tenantID, kbID)
		if err != nil {
			return err
		}
		if rootSelected && counts[types.FolderRootID] != 0 {
			return types.ErrFolderNotEmpty
		}
		for _, id := range toDelete {
			if counts[id] != 0 {
				return types.ErrFolderNotEmpty
			}
		}
		return repo.DeleteSubtree(ctx, tenantID, kbID, toDelete)
	})
}

func normalizeRequiredIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// MoveBatchToFolder validates every document and folder before making any write,
// then applies all moves under one KB structural lock and transaction.
func (s *knowledgeFolderService) MoveBatchToFolder(
	ctx context.Context, kbID string, knowledgeIDs, folderIDs []string, targetFolderID string,
) error {
	tenantID, err := s.scope(ctx, kbID)
	if err != nil {
		return err
	}
	knowledgeIDs = normalizeRequiredIDs(knowledgeIDs)
	folderIDs = normalizeRequiredIDs(folderIDs)
	if len(knowledgeIDs) == 0 && len(folderIDs) == 0 {
		return types.ErrInvalidArgument
	}
	for _, id := range folderIDs {
		if id == targetFolderID {
			return types.ErrInvalidArgument
		}
	}
	return s.folders.Transaction(ctx, func(repo interfaces.KnowledgeFolderRepository) error {
		if err := repo.LockKnowledgeBase(ctx, tenantID, kbID); err != nil {
			return err
		}
		all, err := repo.ListAllForUpdate(ctx, tenantID, kbID)
		if err != nil {
			return err
		}
		byID := make(map[string]*types.KnowledgeFolder, len(all))
		for _, folder := range all {
			byID[folder.ID] = folder
		}
		var target *types.KnowledgeFolder
		if targetFolderID != types.FolderRootID {
			target = byID[targetFolderID]
			if target == nil {
				return repository.ErrKnowledgeFolderNotFound
			}
		}
		selected := make(map[string]struct{}, len(folderIDs))
		for _, id := range folderIDs {
			if byID[id] == nil {
				return repository.ErrKnowledgeFolderNotFound
			}
			selected[id] = struct{}{}
		}
		// Build the final target sibling-name set before any mutation. Every
		// selected folder ends at target; unselected target children remain.
		finalNames := make(map[string]string, len(folderIDs))
		for _, candidate := range all {
			if candidate.ParentID != targetFolderID {
				continue
			}
			if _, moving := selected[candidate.ID]; moving {
				continue
			}
			finalNames[candidate.Name] = candidate.ID
		}
		for _, id := range folderIDs {
			folder := byID[id]
			if existingID, exists := finalNames[folder.Name]; exists && existingID != id {
				return types.ErrFolderAlreadyExists
			}
			finalNames[folder.Name] = id
		}
		for _, id := range folderIDs {
			folder := byID[id]
			if target != nil && strings.HasPrefix(target.Path+"/", folder.Path+"/") {
				return types.ErrInvalidArgument
			}
			for _, other := range folderIDs {
				if other != id && strings.HasPrefix(byID[other].Path+"/", folder.Path+"/") {
					return types.ErrInvalidArgument
				}
			}
			newDepth := 1
			if target != nil {
				newDepth = target.Depth + 1
			}
			maxDepth := folder.Depth
			for _, candidate := range all {
				if candidate.ID == folder.ID || strings.HasPrefix(candidate.Path, folder.Path+"/") {
					if candidate.Depth > maxDepth {
						maxDepth = candidate.Depth
					}
				}
			}
			if maxDepth+(newDepth-folder.Depth) > types.MaxFolderDepth {
				return types.ErrInvalidArgument
			}
		}
		knowledges, err := repo.GetKnowledgeBatchForUpdate(ctx, tenantID, knowledgeIDs)
		if err != nil {
			return err
		}
		if len(knowledges) != len(knowledgeIDs) {
			return repository.ErrKnowledgeNotFound
		}
		for _, knowledge := range knowledges {
			if knowledge.KnowledgeBaseID != kbID {
				return repository.ErrKnowledgeFolderNotFound
			}
		}

		for _, id := range folderIDs {
			folder := byID[id]
			if folder.ParentID == targetFolderID {
				continue
			}
			newDepth := 1
			if target != nil {
				newDepth = target.Depth + 1
			}
			oldDepth := folder.Depth
			oldPath := folder.Path
			newPath := folderPath(target, folder.ID)
			folder.ParentID, folder.Path, folder.Depth = targetFolderID, newPath, newDepth
			if err := repo.MoveSubtree(ctx, folder, oldPath, newPath, newDepth-oldDepth); err != nil {
				return err
			}
		}
		return repo.MoveKnowledgeToFolder(ctx, tenantID, kbID, knowledgeIDs, targetFolderID)
	})
}
