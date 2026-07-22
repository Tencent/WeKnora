package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func parseKnowledgeFolderPath(folder *types.KnowledgeFolder) ([]string, error) {
	pathIDs, err := types.ValidateKnowledgeFolderStructure(folder)
	if err != nil {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	return pathIDs, nil
}

func loadValidatedKnowledgeFolderChain(
	ctx context.Context,
	repo interfaces.KnowledgeFolderReader,
	tenantID uint64,
	kbID string,
	folder *types.KnowledgeFolder,
) ([]*types.KnowledgeFolder, error) {
	if folder == nil ||
		folder.TenantID != tenantID ||
		folder.KnowledgeBaseID != kbID {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	pathIDs, err := parseKnowledgeFolderPath(folder)
	if err != nil {
		return nil, err
	}
	folders, err := repo.ListByIDs(ctx, tenantID, kbID, pathIDs)
	if err != nil {
		return nil, err
	}
	if len(folders) != len(pathIDs) {
		return nil, ErrKnowledgeFolderDataIntegrity
	}

	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, candidate := range folders {
		if _, validationErr := types.ValidateKnowledgeFolderStructure(candidate); validationErr != nil ||
			candidate.TenantID != tenantID ||
			candidate.KnowledgeBaseID != kbID {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, exists := byID[candidate.ID]; exists {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		byID[candidate.ID] = candidate
	}

	chain := make([]*types.KnowledgeFolder, 0, len(pathIDs))
	expectedPath := ""
	for index, pathID := range pathIDs {
		candidate, ok := byID[pathID]
		if !ok {
			return nil, ErrKnowledgeFolderDataIntegrity
		}

		expectedParentID := types.KnowledgeFolderRootID
		if index > 0 {
			expectedParentID = pathIDs[index-1]
		}
		expectedPath = knowledgeFolderChildPath(expectedPath, pathID)
		if candidate.ParentID != expectedParentID ||
			candidate.Depth != index+1 ||
			candidate.Path != expectedPath {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		chain = append(chain, candidate)
	}
	return chain, nil
}

func loadValidatedKnowledgeFolderSubtree(
	ctx context.Context,
	repo interfaces.KnowledgeFolderReader,
	tenantID uint64,
	kbID string,
	root *types.KnowledgeFolder,
) ([]*types.KnowledgeFolder, error) {
	rootID := types.KnowledgeFolderRootID
	pathPrefix := ""
	if root != nil {
		if _, err := parseKnowledgeFolderPath(root); err != nil ||
			root.TenantID != tenantID ||
			root.KnowledgeBaseID != kbID {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		rootID = root.ID
		pathPrefix = root.Path
	}

	folders, err := repo.ListSubtreeFolders(ctx, tenantID, kbID, rootID, pathPrefix)
	if err != nil {
		return nil, err
	}
	if err = validateKnowledgeFolderSubtree(tenantID, kbID, root, folders); err != nil {
		return nil, err
	}
	return folders, nil
}

func validateKnowledgeFolderSubtree(
	tenantID uint64,
	kbID string,
	root *types.KnowledgeFolder,
	folders []*types.KnowledgeFolder,
) error {
	if root != nil && len(folders) == 0 {
		return ErrKnowledgeFolderDataIntegrity
	}

	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	childrenByParent := make(map[string][]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil ||
			folder.TenantID != tenantID ||
			folder.KnowledgeBaseID != kbID {
			return ErrKnowledgeFolderDataIntegrity
		}
		if _, exists := byID[folder.ID]; exists {
			return ErrKnowledgeFolderDataIntegrity
		}
		byID[folder.ID] = folder
		childrenByParent[folder.ParentID] = append(childrenByParent[folder.ParentID], folder)
	}

	queue := make([]*types.KnowledgeFolder, 0, len(folders))
	if root == nil {
		queue = append(queue, childrenByParent[types.KnowledgeFolderRootID]...)
	} else {
		persistedRoot, ok := byID[root.ID]
		if !ok ||
			persistedRoot.ParentID != root.ParentID ||
			persistedRoot.Path != root.Path ||
			persistedRoot.Depth != root.Depth {
			return ErrKnowledgeFolderDataIntegrity
		}
		queue = append(queue, persistedRoot)
	}

	visited := make(map[string]struct{}, len(folders))
	for len(queue) > 0 {
		folder := queue[0]
		queue = queue[1:]
		if _, exists := visited[folder.ID]; exists {
			return ErrKnowledgeFolderDataIntegrity
		}
		visited[folder.ID] = struct{}{}

		for _, child := range childrenByParent[folder.ID] {
			if child.Depth != folder.Depth+1 ||
				child.Path != knowledgeFolderChildPath(folder.Path, child.ID) {
				return ErrKnowledgeFolderDataIntegrity
			}
			queue = append(queue, child)
		}
	}
	if len(visited) != len(folders) {
		return ErrKnowledgeFolderDataIntegrity
	}
	return nil
}

func validateKnowledgeFolderDirectChildren(
	tenantID uint64,
	kbID string,
	parent *types.KnowledgeFolder,
	folders []*types.KnowledgeFolder,
) error {
	expectedParentID := types.KnowledgeFolderRootID
	expectedParentPath := ""
	expectedDepth := 1
	if parent != nil {
		expectedParentID = parent.ID
		expectedParentPath = parent.Path
		expectedDepth = parent.Depth + 1
	}

	seen := make(map[string]struct{}, len(folders))
	for _, folder := range folders {
		if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil ||
			folder.TenantID != tenantID ||
			folder.KnowledgeBaseID != kbID ||
			folder.ParentID != expectedParentID ||
			folder.Depth != expectedDepth ||
			folder.Path != knowledgeFolderChildPath(expectedParentPath, folder.ID) {
			return ErrKnowledgeFolderDataIntegrity
		}
		if _, exists := seen[folder.ID]; exists {
			return ErrKnowledgeFolderDataIntegrity
		}
		seen[folder.ID] = struct{}{}
	}
	return nil
}

func knowledgeFolderChildPath(parentPath string, folderID string) string {
	if parentPath == "" {
		return "/" + folderID + "/"
	}
	return parentPath + folderID + "/"
}
