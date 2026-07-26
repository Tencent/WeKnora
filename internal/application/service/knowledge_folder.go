package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrKnowledgeFolderInvalidName = errors.New("knowledge folder name is invalid")
	ErrKnowledgeFolderDepthLimit  = errors.New("knowledge folder depth exceeds 10 levels")
	ErrKnowledgeFolderCycle       = errors.New("knowledge folder cannot be moved into itself or its descendant")
)

type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
}

func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo}
}

func (s *knowledgeFolderService) Create(
	ctx context.Context, kbID string, req *types.KnowledgeFolderCreateRequest,
) (*types.KnowledgeFolder, error) {
	if req == nil {
		return nil, ErrKnowledgeFolderInvalidName
	}
	name, err := normalizeKnowledgeFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	parentID := normalizeFolderID(req.ParentFolderID)
	depth := 1
	if parentID != nil {
		parent, err := s.repo.GetByID(ctx, tenantID, kbID, *parentID)
		if err != nil {
			return nil, err
		}
		depth = parent.Depth + 1
	}
	if depth > types.MaxKnowledgeFolderDepth {
		return nil, ErrKnowledgeFolderDepthLimit
	}
	if existing, err := s.repo.GetChildByName(ctx, tenantID, kbID, parentID, name); err == nil && existing != nil {
		return nil, repository.ErrKnowledgeFolderConflict
	} else if err != nil && !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}
	creatorID, _ := types.UserIDFromContext(ctx)
	now := time.Now()
	folder := &types.KnowledgeFolder{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentFolderID:  parentID,
		Name:            name,
		Description:     strings.TrimSpace(req.Description),
		Depth:           depth,
		CreatorID:       creatorID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.Create(ctx, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) List(ctx context.Context, kbID string) ([]types.KnowledgeFolderNode, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByKnowledgeBase(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	counts, err := s.repo.CountKnowledgeByFolder(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	children := make(map[string][]string)
	for _, folder := range folders {
		if folder.ParentFolderID != nil {
			children[*folder.ParentFolderID] = append(children[*folder.ParentFolderID], folder.ID)
		}
	}
	memo := make(map[string]int64, len(folders))
	var recursiveCount func(string) int64
	recursiveCount = func(id string) int64 {
		if count, ok := memo[id]; ok {
			return count
		}
		count := counts[id]
		for _, childID := range children[id] {
			count += recursiveCount(childID)
		}
		memo[id] = count
		return count
	}
	nodes := make([]types.KnowledgeFolderNode, 0, len(folders))
	for _, folder := range folders {
		nodes = append(nodes, types.KnowledgeFolderNode{
			KnowledgeFolder:         *folder,
			DirectKnowledgeCount:    counts[folder.ID],
			RecursiveKnowledgeCount: recursiveCount(folder.ID),
			HasChildren:             len(children[folder.ID]) > 0,
		})
	}
	return nodes, nil
}

func (s *knowledgeFolderService) Update(
	ctx context.Context, kbID, folderID string, req *types.KnowledgeFolderUpdateRequest,
) (*types.KnowledgeFolder, error) {
	if req == nil {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByKnowledgeBase(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	children := make(map[string][]*types.KnowledgeFolder)
	for _, folder := range folders {
		byID[folder.ID] = folder
		if folder.ParentFolderID != nil {
			children[*folder.ParentFolderID] = append(children[*folder.ParentFolderID], folder)
		}
	}
	folder := byID[folderID]
	if folder == nil {
		return nil, repository.ErrKnowledgeFolderNotFound
	}

	name := folder.Name
	if req.Name != nil {
		name, err = normalizeKnowledgeFolderName(*req.Name)
		if err != nil {
			return nil, err
		}
	}
	targetParentID := folder.ParentFolderID
	moveRequested := req.ParentFolderID != nil || req.MoveToRoot
	if req.MoveToRoot {
		targetParentID = nil
	} else if req.ParentFolderID != nil {
		targetParentID = normalizeFolderID(req.ParentFolderID)
	}
	if targetParentID != nil {
		if *targetParentID == folder.ID {
			return nil, ErrKnowledgeFolderCycle
		}
		if byID[*targetParentID] == nil {
			return nil, repository.ErrKnowledgeFolderNotFound
		}
	}

	descendants := collectFolderDescendants(folder.ID, children)
	if targetParentID != nil {
		for _, descendant := range descendants {
			if descendant.ID == *targetParentID {
				return nil, ErrKnowledgeFolderCycle
			}
		}
	}
	if existing, err := s.repo.GetChildByName(ctx, tenantID, kbID, targetParentID, name); err == nil {
		if existing.ID != folder.ID {
			return nil, repository.ErrKnowledgeFolderConflict
		}
	} else if !errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil, err
	}

	newDepth := 1
	if targetParentID != nil {
		newDepth = byID[*targetParentID].Depth + 1
	}
	depthDelta := newDepth - folder.Depth
	maxDepth := newDepth
	for _, descendant := range descendants {
		candidate := descendant.Depth + depthDelta
		if candidate > maxDepth {
			maxDepth = candidate
		}
	}
	if maxDepth > types.MaxKnowledgeFolderDepth {
		return nil, ErrKnowledgeFolderDepthLimit
	}

	now := time.Now()
	folder.Name = name
	if req.Description != nil {
		folder.Description = strings.TrimSpace(*req.Description)
	}
	if moveRequested {
		folder.ParentFolderID = targetParentID
	}
	if req.UpdateSortOrder {
		folder.SortOrder = req.SortOrder
	}
	folder.Depth = newDepth
	folder.UpdatedAt = now
	updates := []*types.KnowledgeFolder{folder}
	for _, descendant := range descendants {
		descendant.Depth += depthDelta
		descendant.UpdatedAt = now
		updates = append(updates, descendant)
	}
	if err := s.repo.UpdateTree(ctx, updates); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) Delete(ctx context.Context, kbID, folderID string) error {
	return s.repo.DeleteEmpty(ctx, types.MustTenantIDFromContext(ctx), kbID, folderID)
}

func (s *knowledgeFolderService) DeleteRecursive(ctx context.Context, kbID, folderID string) error {
	return s.repo.DeleteTree(ctx, types.MustTenantIDFromContext(ctx), kbID, folderID)
}

func (s *knowledgeFolderService) DeleteByKnowledgeBase(ctx context.Context, kbID string) error {
	return s.repo.DeleteByKnowledgeBase(ctx, types.MustTenantIDFromContext(ctx), kbID)
}

func (s *knowledgeFolderService) MoveKnowledge(
	ctx context.Context, kbID string, knowledgeIDs []string, folderID *string,
) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	folderID = normalizeFolderID(folderID)
	if err := s.ValidatePlacement(ctx, tenantID, kbID, folderID); err != nil {
		return err
	}
	return s.repo.MoveKnowledge(ctx, tenantID, kbID, uniqueFolderStrings(knowledgeIDs), folderID)
}

func (s *knowledgeFolderService) ValidatePlacement(
	ctx context.Context, tenantID uint64, kbID string, folderID *string,
) error {
	folderID = normalizeFolderID(folderID)
	if folderID == nil {
		return nil
	}
	_, err := s.repo.GetByID(ctx, tenantID, kbID, *folderID)
	return err
}

func (s *knowledgeFolderService) ResolveKnowledgeIDs(
	ctx context.Context, tenantID uint64, scope types.FolderScope,
) ([]string, error) {
	if strings.TrimSpace(scope.KnowledgeBaseID) == "" || strings.TrimSpace(scope.FolderID) == "" {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	if _, err := s.repo.GetByID(ctx, tenantID, scope.KnowledgeBaseID, scope.FolderID); err != nil {
		return nil, err
	}
	ids, err := s.repo.ListKnowledgeIDsByScope(
		ctx, tenantID, scope.KnowledgeBaseID, scope.FolderID, scope.IncludeDescendants,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve knowledge folder scope: %w", err)
	}
	return uniqueFolderStrings(ids), nil
}

func normalizeKnowledgeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 || strings.ContainsAny(name, "/\\") {
		return "", ErrKnowledgeFolderInvalidName
	}
	return name, nil
}

func normalizeFolderID(folderID *string) *string {
	if folderID == nil {
		return nil
	}
	value := strings.TrimSpace(*folderID)
	if value == "" {
		return nil
	}
	return &value
}

func collectFolderDescendants(
	folderID string, children map[string][]*types.KnowledgeFolder,
) []*types.KnowledgeFolder {
	var result []*types.KnowledgeFolder
	queue := append([]*types.KnowledgeFolder(nil), children[folderID]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)
		queue = append(queue, children[current.ID]...)
	}
	return result
}

func uniqueFolderStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
