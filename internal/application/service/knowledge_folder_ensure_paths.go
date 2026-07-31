package service

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	knowledgeFolderEnsurePathsMaxPaths          = 200
	knowledgeFolderEnsurePathsMaxTotalSegments  = 2000
	knowledgeFolderEnsurePathsMaxUniqueNodes    = 1000
	knowledgeFolderEnsurePathsMaxClientKeyRunes = 512
)

type knowledgeFolderEnsurePathNode struct {
	name        string
	candidateID string
	children    map[string]*knowledgeFolderEnsurePathNode
	childOrder  []*knowledgeFolderEnsurePathNode
}

type knowledgeFolderEnsurePathPlanItem struct {
	clientKey    string
	segmentCount int
	terminal     *knowledgeFolderEnsurePathNode
}

type knowledgeFolderEnsurePathsPlan struct {
	parentID string
	root     *knowledgeFolderEnsurePathNode
	nodes    []*knowledgeFolderEnsurePathNode
	items    []knowledgeFolderEnsurePathPlanItem
}

type knowledgeFolderEnsurePathWork struct {
	node   *knowledgeFolderEnsurePathNode
	folder *types.KnowledgeFolder
}

// EnsurePaths atomically resolves or creates normalized folder paths.
func (s *knowledgeFolderService) EnsurePaths(
	ctx context.Context,
	kbID string,
	req *types.KnowledgeFolderEnsurePathsRequest,
) ([]types.KnowledgeFolderEnsurePathResult, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	plan, err := newKnowledgeFolderEnsurePathsPlan(req)
	if err != nil {
		return nil, err
	}

	var successfulResolved map[*knowledgeFolderEnsurePathNode]*types.KnowledgeFolder
	err = s.repo.RunTreeWriteTransaction(
		ctx,
		tenantID,
		kbID,
		func(txRepo interfaces.KnowledgeFolderTreeRepository) error {
			resolved, attemptErr := executeKnowledgeFolderEnsurePathsAttempt(
				ctx,
				txRepo,
				tenantID,
				kbID,
				plan,
			)
			if attemptErr != nil {
				return attemptErr
			}
			// SQLite may replay this callback; publish only a complete attempt.
			successfulResolved = resolved
			return nil
		},
	)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	return buildKnowledgeFolderEnsurePathResults(plan, successfulResolved)
}

func newKnowledgeFolderEnsurePathsPlan(
	req *types.KnowledgeFolderEnsurePathsRequest,
) (*knowledgeFolderEnsurePathsPlan, error) {
	if req == nil ||
		len(req.Paths) < 1 ||
		len(req.Paths) > knowledgeFolderEnsurePathsMaxPaths {
		return nil, ErrKnowledgeFolderInvalidArgument
	}

	parentID, err := normalizeKnowledgeFolderID(req.ParentID, "parent_id", true)
	if err != nil {
		return nil, err
	}
	if parentID != req.ParentID {
		return nil, ErrKnowledgeFolderInvalidArgument
	}

	seenClientKeys := make(map[string]struct{}, len(req.Paths))
	totalSegments := 0
	for _, path := range req.Paths {
		if err = validateKnowledgeFolderEnsurePathClientKey(path.ClientKey); err != nil {
			return nil, err
		}
		if _, exists := seenClientKeys[path.ClientKey]; exists {
			return nil, ErrKnowledgeFolderInvalidArgument
		}
		seenClientKeys[path.ClientKey] = struct{}{}

		if len(path.Segments) < 1 || len(path.Segments) > types.KnowledgeFolderMaxDepth {
			return nil, ErrKnowledgeFolderInvalidArgument
		}
		totalSegments += len(path.Segments)
		if totalSegments > knowledgeFolderEnsurePathsMaxTotalSegments {
			return nil, ErrKnowledgeFolderInvalidArgument
		}
	}

	plan := &knowledgeFolderEnsurePathsPlan{
		parentID: parentID,
		root: &knowledgeFolderEnsurePathNode{
			children: make(map[string]*knowledgeFolderEnsurePathNode),
		},
		items: make([]knowledgeFolderEnsurePathPlanItem, 0, len(req.Paths)),
	}
	for _, path := range req.Paths {
		current := plan.root
		for _, segment := range path.Segments {
			name, nameErr := normalizeKnowledgeFolderName(segment)
			if nameErr != nil {
				return nil, nameErr
			}
			child := current.children[name]
			if child == nil {
				if len(plan.nodes) == knowledgeFolderEnsurePathsMaxUniqueNodes {
					return nil, ErrKnowledgeFolderInvalidArgument
				}
				child = &knowledgeFolderEnsurePathNode{
					name:     name,
					children: make(map[string]*knowledgeFolderEnsurePathNode),
				}
				current.children[name] = child
				current.childOrder = append(current.childOrder, child)
				plan.nodes = append(plan.nodes, child)
			}
			current = child
		}
		plan.items = append(plan.items, knowledgeFolderEnsurePathPlanItem{
			clientKey:    path.ClientKey,
			segmentCount: len(path.Segments),
			terminal:     current,
		})
	}

	// Candidate IDs stay stable across SQLite transaction callback replays.
	for _, node := range plan.nodes {
		node.candidateID = uuid.NewString()
	}
	return plan, nil
}

func validateKnowledgeFolderEnsurePathClientKey(clientKey string) error {
	if !utf8.ValidString(clientKey) ||
		clientKey == "" ||
		strings.TrimSpace(clientKey) != clientKey ||
		utf8.RuneCountInString(clientKey) > knowledgeFolderEnsurePathsMaxClientKeyRunes {
		return ErrKnowledgeFolderInvalidArgument
	}
	for _, char := range clientKey {
		if char == '\x00' || unicode.IsControl(char) {
			return ErrKnowledgeFolderInvalidArgument
		}
	}
	return nil
}

func executeKnowledgeFolderEnsurePathsAttempt(
	ctx context.Context,
	txRepo interfaces.KnowledgeFolderTreeRepository,
	tenantID uint64,
	kbID string,
	plan *knowledgeFolderEnsurePathsPlan,
) (map[*knowledgeFolderEnsurePathNode]*types.KnowledgeFolder, error) {
	var parent *types.KnowledgeFolder
	if plan.parentID != types.KnowledgeFolderRootID {
		persistedParent, err := txRepo.GetByID(ctx, tenantID, kbID, plan.parentID)
		if err != nil {
			return nil, err
		}
		chain, err := loadValidatedKnowledgeFolderChain(
			ctx,
			txRepo,
			tenantID,
			kbID,
			persistedParent,
		)
		if err != nil {
			return nil, err
		}
		for _, ancestor := range chain {
			if ancestor == nil || ancestor.DeletedAt.Valid {
				return nil, ErrKnowledgeFolderDataIntegrity
			}
		}
		parent = chain[len(chain)-1]
	}

	parentDepth := 0
	if parent != nil {
		parentDepth = parent.Depth
	}
	for _, item := range plan.items {
		if parentDepth+item.segmentCount > types.KnowledgeFolderMaxDepth {
			return nil, ErrKnowledgeFolderDepthExceeded
		}
	}

	resolved := make(map[*knowledgeFolderEnsurePathNode]*types.KnowledgeFolder, len(plan.nodes))
	queue := []knowledgeFolderEnsurePathWork{{node: plan.root, folder: parent}}
	for len(queue) > 0 {
		work := queue[0]
		queue = queue[1:]
		if len(work.node.childOrder) == 0 {
			continue
		}

		parentID := types.KnowledgeFolderRootID
		parentPath := ""
		parentDepth := 0
		if work.folder != nil {
			parentID = work.folder.ID
			parentPath = work.folder.Path
			parentDepth = work.folder.Depth
		}
		names := make([]string, len(work.node.childOrder))
		for index, child := range work.node.childOrder {
			names[index] = child.name
		}

		existing, err := txRepo.ListByParentAndNames(
			ctx,
			tenantID,
			kbID,
			parentID,
			names,
		)
		if err != nil {
			return nil, err
		}
		byName, err := validateKnowledgeFolderEnsurePathChildren(
			tenantID,
			kbID,
			work.folder,
			names,
			existing,
		)
		if err != nil {
			return nil, err
		}

		ids := make(map[string]string, len(work.node.childOrder))
		for name, folder := range byName {
			ids[folder.ID] = name
		}
		for _, child := range work.node.childOrder {
			folder := byName[child.name]
			if folder == nil {
				folder = &types.KnowledgeFolder{
					ID:              child.candidateID,
					TenantID:        tenantID,
					KnowledgeBaseID: kbID,
					ParentID:        parentID,
					Name:            child.name,
					Path:            knowledgeFolderChildPath(parentPath, child.candidateID),
					Depth:           parentDepth + 1,
					SortOrder:       0,
				}
				created, createErr := txRepo.CreateIfAbsent(ctx, folder)
				if createErr != nil {
					return nil, createErr
				}
				if !created {
					folder, err = rereadKnowledgeFolderEnsurePathChild(
						ctx,
						txRepo,
						tenantID,
						kbID,
						work.folder,
						child.name,
					)
					if err != nil {
						return nil, err
					}
				}
			}

			if otherName, exists := ids[folder.ID]; exists && otherName != child.name {
				return nil, ErrKnowledgeFolderDataIntegrity
			}
			ids[folder.ID] = child.name
			resolved[child] = folder
			if len(child.childOrder) > 0 {
				queue = append(queue, knowledgeFolderEnsurePathWork{node: child, folder: folder})
			}
		}
	}
	return resolved, nil
}

func validateKnowledgeFolderEnsurePathChildren(
	tenantID uint64,
	kbID string,
	parent *types.KnowledgeFolder,
	names []string,
	folders []*types.KnowledgeFolder,
) (map[string]*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderDirectChildren(tenantID, kbID, parent, folders); err != nil {
		return nil, err
	}
	expectedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		expectedNames[name] = struct{}{}
	}
	byName := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		if folder.DeletedAt.Valid {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, expected := expectedNames[folder.Name]; !expected {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, duplicate := byName[folder.Name]; duplicate {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		byName[folder.Name] = folder
	}
	return byName, nil
}

func rereadKnowledgeFolderEnsurePathChild(
	ctx context.Context,
	txRepo interfaces.KnowledgeFolderTreeRepository,
	tenantID uint64,
	kbID string,
	parent *types.KnowledgeFolder,
	name string,
) (*types.KnowledgeFolder, error) {
	parentID := types.KnowledgeFolderRootID
	if parent != nil {
		parentID = parent.ID
	}
	folder, err := txRepo.GetByParentAndName(ctx, tenantID, kbID, parentID, name)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeFolderNotFound) ||
			errors.Is(err, ErrKnowledgeFolderNotFound) {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		return nil, err
	}
	byName, err := validateKnowledgeFolderEnsurePathChildren(
		tenantID,
		kbID,
		parent,
		[]string{name},
		[]*types.KnowledgeFolder{folder},
	)
	if err != nil {
		return nil, err
	}
	validated := byName[name]
	if validated == nil {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	return validated, nil
}

func buildKnowledgeFolderEnsurePathResults(
	plan *knowledgeFolderEnsurePathsPlan,
	resolved map[*knowledgeFolderEnsurePathNode]*types.KnowledgeFolder,
) ([]types.KnowledgeFolderEnsurePathResult, error) {
	if plan == nil || len(resolved) != len(plan.nodes) {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	results := make([]types.KnowledgeFolderEnsurePathResult, len(plan.items))
	for index, item := range plan.items {
		folder := resolved[item.terminal]
		if folder == nil {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		folderID, err := normalizeKnowledgeFolderID(folder.ID, "folder_id", false)
		if err != nil || folderID != folder.ID {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		results[index] = types.KnowledgeFolderEnsurePathResult{
			ClientKey: item.clientKey,
			FolderID:  folder.ID,
		}
	}
	return results, nil
}
