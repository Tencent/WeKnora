package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// KnowledgeScopeLimits bounds request selectors and resolved folder IDs.
type KnowledgeScopeLimits struct {
	MaxSelectors         int
	MaxResolvedFolderIDs int
}

type knowledgeScopeResolver struct {
	repository interfaces.KnowledgeFolderScopeRepository
	limits     KnowledgeScopeLimits
}

type knowledgeScopeFolderSelector struct {
	id        string
	recursive bool
}

var _ interfaces.KnowledgeScopeResolver = (*knowledgeScopeResolver)(nil)

// NewKnowledgeScopeResolver creates a folder-aware execution scope resolver.
func NewKnowledgeScopeResolver(
	scopeRepository interfaces.KnowledgeFolderScopeRepository,
	limits KnowledgeScopeLimits,
) (interfaces.KnowledgeScopeResolver, error) {
	if isNilKnowledgeScopeDependency(scopeRepository) {
		return nil, fmt.Errorf(
			"%w: folder scope repository is nil",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	maxInt := int(^uint(0) >> 1)
	if limits.MaxSelectors <= 0 ||
		limits.MaxResolvedFolderIDs <= 0 ||
		limits.MaxResolvedFolderIDs > maxInt-1 {
		return nil, fmt.Errorf(
			"%w: knowledge scope limits are invalid",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	return &knowledgeScopeResolver{
		repository: scopeRepository,
		limits:     limits,
	}, nil
}

// Resolve builds an immutable execution scope from already-authorized targets.
func (s *knowledgeScopeResolver) Resolve(
	ctx context.Context,
	input types.KnowledgeScopeResolveInput,
) (*types.KnowledgeScope, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"%w: context is nil",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || isNilKnowledgeScopeDependency(s.repository) ||
		s.limits.MaxSelectors <= 0 ||
		s.limits.MaxResolvedFolderIDs <= 0 {
		return nil, fmt.Errorf(
			"%w: knowledge scope resolver is not initialized",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}

	request, err := types.NormalizeKnowledgeScopeRequest(input.Request)
	if err != nil {
		return nil, err
	}
	authorizedTargets, authorizedTargetIndexes, err :=
		validateAuthorizedKnowledgeScopeTargets(
			input.AuthorizedTargets,
		)
	if err != nil {
		return nil, err
	}
	if err = validateKnowledgeScopeFolderTargets(
		request,
		authorizedTargetIndexes,
	); err != nil {
		return nil, err
	}
	folderScopes, selectorCount := indexKnowledgeScopeFolderSelectors(request)
	if err = s.validateFolderSelectorCount(selectorCount); err != nil {
		return nil, err
	}

	resolvedTargets := make([]types.KnowledgeScopeTarget, 0, len(authorizedTargets))
	totalResolvedFolderIDs := 0
	requestFolderScopesProvided := request != nil && request.FolderScopes != nil
	for _, authorized := range authorizedTargets {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		remaining := s.limits.MaxResolvedFolderIDs - totalResolvedFolderIDs
		selectors, hasFolderEntry := folderScopes[authorized.KnowledgeBaseID()]
		filter, resolveErr := s.resolveFolderFilter(
			ctx,
			requestFolderScopesProvided,
			hasFolderEntry,
			selectors,
			authorized.SourceTenantID(),
			authorized.KnowledgeBaseID(),
			remaining,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if filter.Enabled() {
			resolvedCount := len(filter.FolderIDs())
			if resolvedCount > remaining {
				return nil, fmt.Errorf(
					"%w: resolved folder limit exceeded",
					types.ErrInvalidKnowledgeScopeRequest,
				)
			}
			totalResolvedFolderIDs += resolvedCount
		}

		target, targetErr := types.NewKnowledgeScopeTarget(
			authorized.KnowledgeBaseID(),
			authorized.SourceTenantID(),
			authorized.KnowledgeIDs(),
			authorized.TagIDs(),
			authorized.ScopeTagIDs(),
			filter,
		)
		if targetErr != nil {
			return nil, targetErr
		}
		resolvedTargets = append(resolvedTargets, target)
	}

	return types.NewKnowledgeScope(resolvedTargets)
}

// ValidateFolderSelectorBudget applies the configured Phase 4A request budget
// without reading folder metadata.
func (s *knowledgeScopeResolver) ValidateFolderSelectorBudget(
	request *types.KnowledgeScopeRequest,
) error {
	if s == nil || s.limits.MaxSelectors <= 0 {
		return fmt.Errorf(
			"%w: knowledge scope resolver is not initialized",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	normalized, err := types.NormalizeKnowledgeScopeRequest(request)
	if err != nil {
		return err
	}
	_, selectorCount := indexKnowledgeScopeFolderSelectors(normalized)
	return s.validateFolderSelectorCount(selectorCount)
}

func (s *knowledgeScopeResolver) validateFolderSelectorCount(
	selectorCount int,
) error {
	if selectorCount > s.limits.MaxSelectors {
		return fmt.Errorf(
			"%w: folder selector limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	return nil
}

func validateAuthorizedKnowledgeScopeTargets(
	input []types.AuthorizedKnowledgeScopeTarget,
) (
	[]types.KnowledgeScopeTarget,
	map[string]int,
	error,
) {
	disabled, err := types.NewResolvedFolderFilter(false, nil)
	if err != nil {
		return nil, nil, err
	}
	targets := make([]types.KnowledgeScopeTarget, 0, len(input))
	seenKnowledgeBases := make(map[string]struct{}, len(input))
	for _, authorized := range input {
		target, targetErr := types.NewKnowledgeScopeTarget(
			authorized.KnowledgeBaseID,
			authorized.SourceTenantID,
			authorized.KnowledgeIDs,
			authorized.TagIDs,
			authorized.ScopeTagIDs,
			disabled,
		)
		if targetErr != nil {
			return nil, nil, targetErr
		}
		if _, exists := seenKnowledgeBases[target.KnowledgeBaseID()]; exists {
			return nil, nil, fmt.Errorf(
				"%w: ambiguous authorized knowledge base target",
				types.ErrInvalidKnowledgeScopeRequest,
			)
		}
		seenKnowledgeBases[target.KnowledgeBaseID()] = struct{}{}
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(left int, right int) bool {
		if targets[left].SourceTenantID() != targets[right].SourceTenantID() {
			return targets[left].SourceTenantID() <
				targets[right].SourceTenantID()
		}
		return targets[left].KnowledgeBaseID() <
			targets[right].KnowledgeBaseID()
	})
	indexes := make(map[string]int, len(targets))
	for index, target := range targets {
		indexes[target.KnowledgeBaseID()] = index
	}
	return targets, indexes, nil
}

func validateKnowledgeScopeFolderTargets(
	request *types.KnowledgeScopeRequest,
	authorizedTargetIndexes map[string]int,
) error {
	if request == nil || request.FolderScopes == nil {
		return nil
	}
	for _, folderScope := range *request.FolderScopes {
		if _, exists := authorizedTargetIndexes[folderScope.KnowledgeBaseID]; !exists {
			return fmt.Errorf(
				"%w: folder scope has no authorized target",
				types.ErrInvalidKnowledgeScopeRequest,
			)
		}
	}
	return nil
}

func indexKnowledgeScopeFolderSelectors(
	request *types.KnowledgeScopeRequest,
) (map[string][]knowledgeScopeFolderSelector, int) {
	indexed := make(map[string][]knowledgeScopeFolderSelector)
	if request == nil || request.FolderScopes == nil {
		return indexed, 0
	}
	count := 0
	for _, scope := range *request.FolderScopes {
		if _, exists := indexed[scope.KnowledgeBaseID]; !exists {
			indexed[scope.KnowledgeBaseID] = nil
		}
		recursive := scope.IncludeDescendants == nil ||
			*scope.IncludeDescendants
		for _, folderID := range scope.FolderIDs {
			indexed[scope.KnowledgeBaseID] = append(
				indexed[scope.KnowledgeBaseID],
				knowledgeScopeFolderSelector{
					id:        folderID,
					recursive: recursive,
				},
			)
			count++
		}
	}
	return indexed, count
}

func (s *knowledgeScopeResolver) resolveFolderFilter(
	ctx context.Context,
	requestFolderScopesProvided bool,
	hasFolderEntry bool,
	selectors []knowledgeScopeFolderSelector,
	sourceTenantID uint64,
	knowledgeBaseID string,
	remaining int,
) (types.ResolvedFolderFilter, error) {
	if !requestFolderScopesProvided {
		return types.NewResolvedFolderFilter(false, nil)
	}
	if !hasFolderEntry || len(selectors) == 0 {
		return types.NewResolvedFolderFilter(true, nil)
	}

	rootDirect := false
	rootRecursive := false
	nonRoot := make([]knowledgeScopeFolderSelector, 0, len(selectors))
	for _, selector := range selectors {
		if selector.id == types.KnowledgeFolderRootID {
			if selector.recursive {
				rootRecursive = true
			} else {
				rootDirect = true
			}
			continue
		}
		nonRoot = append(nonRoot, selector)
	}
	if rootRecursive {
		if len(nonRoot) > 0 {
			return types.ResolvedFolderFilter{}, fmt.Errorf(
				"%w: recursive root cannot be mixed with non-root folders",
				types.ErrInvalidKnowledgeScopeRequest,
			)
		}
		// Recursive virtual root represents the whole knowledge base.
		return types.NewResolvedFolderFilter(false, nil)
	}
	if len(nonRoot) == 0 {
		if remaining < 1 {
			return types.ResolvedFolderFilter{}, fmt.Errorf(
				"%w: resolved folder limit exceeded",
				types.ErrInvalidKnowledgeScopeRequest,
			)
		}
		return types.NewResolvedFolderFilter(
			true,
			[]string{types.KnowledgeFolderRootID},
		)
	}
	if remaining <= 0 {
		return types.ResolvedFolderFilter{}, fmt.Errorf(
			"%w: resolved folder limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}

	var attemptFilter types.ResolvedFolderFilter
	attemptReady := false
	err := s.repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		sourceTenantID,
		knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			// SQLite may replay this callback; only the successful attempt is published.
			attemptFilter = types.ResolvedFolderFilter{}
			attemptReady = false

			folderIDs, resolveErr := s.resolveFolderSnapshot(
				ctx,
				reader,
				sourceTenantID,
				knowledgeBaseID,
				nonRoot,
				rootDirect,
				remaining,
			)
			if resolveErr != nil {
				return resolveErr
			}
			filter, filterErr := types.NewResolvedFolderFilter(true, folderIDs)
			if filterErr != nil {
				return filterErr
			}
			attemptFilter = filter
			attemptReady = true
			return nil
		},
	)
	if err != nil {
		return types.ResolvedFolderFilter{},
			mapKnowledgeScopeRepositoryError(ctx, err)
	}
	if !attemptReady {
		return types.ResolvedFolderFilter{}, fmt.Errorf(
			"%w: folder scope snapshot produced no result",
			ErrKnowledgeFolderDataIntegrity,
		)
	}
	return attemptFilter.Clone(), nil
}

func (s *knowledgeScopeResolver) resolveFolderSnapshot(
	ctx context.Context,
	reader interfaces.KnowledgeFolderScopeReader,
	sourceTenantID uint64,
	knowledgeBaseID string,
	selectors []knowledgeScopeFolderSelector,
	rootDirect bool,
	remaining int,
) ([]string, error) {
	if isNilKnowledgeScopeDependency(reader) {
		return nil, fmt.Errorf(
			"%w: folder scope reader is nil",
			ErrKnowledgeFolderDataIntegrity,
		)
	}

	selectedIDs := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		selectedIDs = append(selectedIDs, selector.id)
	}
	sort.Strings(selectedIDs)

	selectedRows, err := reader.ListScopeFoldersByIDs(selectedIDs)
	if err != nil {
		return nil, err
	}
	selectedByID, selectedPaths, err := indexSelectedKnowledgeScopeFolders(
		sourceTenantID,
		knowledgeBaseID,
		selectedIDs,
		selectedRows,
	)
	if err != nil {
		return nil, err
	}

	ancestorIDs := missingKnowledgeScopeAncestorIDs(selectedByID, selectedPaths)
	ancestorRows := []*types.KnowledgeFolder{}
	if len(ancestorIDs) > 0 {
		ancestorRows, err = reader.ListScopeFoldersByIDs(ancestorIDs)
		if err != nil {
			if errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
				return nil, fmt.Errorf(
					"%w: %w",
					ErrKnowledgeFolderDataIntegrity,
					err,
				)
			}
			return nil, err
		}
	}
	allByID, err := mergeKnowledgeScopeAncestorRows(
		sourceTenantID,
		knowledgeBaseID,
		selectedByID,
		ancestorIDs,
		ancestorRows,
	)
	if err != nil {
		return nil, err
	}
	if err = validateKnowledgeScopeFolderChains(allByID, selectedIDs); err != nil {
		return nil, err
	}

	directIDs, roots, rootIDs, err := planKnowledgeScopeFolderResolution(
		selectors,
		selectedByID,
		selectedPaths,
	)
	if err != nil {
		return nil, err
	}
	if rootDirect {
		directIDs = append(directIDs, types.KnowledgeFolderRootID)
		sort.Strings(directIDs)
	}
	if len(directIDs) > remaining {
		return nil, fmt.Errorf(
			"%w: resolved folder limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	if len(roots) == 0 {
		return directIDs, nil
	}
	remainingForSubtree := remaining - len(directIDs)
	if remainingForSubtree <= 0 {
		return nil, fmt.Errorf(
			"%w: resolved folder limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}

	candidates, err := reader.ListScopeSubtreeCandidates(
		roots,
		remainingForSubtree+1,
	)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
			return nil, fmt.Errorf(
				"%w: %w",
				ErrKnowledgeFolderDataIntegrity,
				err,
			)
		}
		return nil, err
	}
	candidateByID, err := indexKnowledgeScopeSubtreeCandidates(
		sourceTenantID,
		knowledgeBaseID,
		candidates,
	)
	if err != nil {
		return nil, err
	}
	if len(candidateByID) > remainingForSubtree {
		return nil, fmt.Errorf(
			"%w: resolved folder limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	uncoveredDirect := make(map[string]struct{}, len(directIDs))
	for _, folderID := range directIDs {
		uncoveredDirect[folderID] = struct{}{}
	}
	for _, selector := range selectors {
		if !selector.recursive {
			if _, remainsDirect := uncoveredDirect[selector.id]; remainsDirect {
				continue
			}
		}
		if _, included := candidateByID[selector.id]; !included {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
	}
	if err = validateKnowledgeScopeSubtree(
		rootIDs,
		selectedByID,
		candidateByID,
	); err != nil {
		return nil, err
	}
	if err = mergeKnowledgeScopeCandidateRows(allByID, candidateByID); err != nil {
		return nil, err
	}
	candidateIDs := make([]string, 0, len(candidateByID))
	for folderID := range candidateByID {
		candidateIDs = append(candidateIDs, folderID)
	}
	if err = validateKnowledgeScopeFolderChains(allByID, candidateIDs); err != nil {
		return nil, err
	}

	resolved := append([]string(nil), directIDs...)
	for folderID := range candidateByID {
		resolved = append(resolved, folderID)
	}
	resolved = stableUniqueKnowledgeScopeIDs(resolved)
	if len(resolved) > remaining {
		return nil, fmt.Errorf(
			"%w: resolved folder limit exceeded",
			types.ErrInvalidKnowledgeScopeRequest,
		)
	}
	return resolved, nil
}

func planKnowledgeScopeFolderResolution(
	selectors []knowledgeScopeFolderSelector,
	selectedByID map[string]*types.KnowledgeFolder,
	selectedPaths map[string][]string,
) (
	[]string,
	[]interfaces.KnowledgeFolderScopeRoot,
	[]string,
	error,
) {
	recursive := make(map[string]struct{}, len(selectors))
	direct := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		if selector.recursive {
			recursive[selector.id] = struct{}{}
		} else {
			direct = append(direct, selector.id)
		}
	}

	minimalRootIDs := make([]string, 0, len(recursive))
	for folderID := range recursive {
		pathIDs, exists := selectedPaths[folderID]
		if !exists || len(pathIDs) == 0 {
			return nil, nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		covered := false
		for _, ancestorID := range pathIDs[:len(pathIDs)-1] {
			if _, recursiveAncestor := recursive[ancestorID]; recursiveAncestor {
				covered = true
				break
			}
		}
		if !covered {
			minimalRootIDs = append(minimalRootIDs, folderID)
		}
	}
	sort.SliceStable(minimalRootIDs, func(left int, right int) bool {
		leftFolder := selectedByID[minimalRootIDs[left]]
		rightFolder := selectedByID[minimalRootIDs[right]]
		if leftFolder == nil || rightFolder == nil {
			return minimalRootIDs[left] < minimalRootIDs[right]
		}
		if leftFolder.Depth != rightFolder.Depth {
			return leftFolder.Depth < rightFolder.Depth
		}
		if leftFolder.Path != rightFolder.Path {
			return leftFolder.Path < rightFolder.Path
		}
		return leftFolder.ID < rightFolder.ID
	})

	minimalRoots := make(map[string]struct{}, len(minimalRootIDs))
	roots := make([]interfaces.KnowledgeFolderScopeRoot, 0, len(minimalRootIDs))
	for _, folderID := range minimalRootIDs {
		folder := selectedByID[folderID]
		if folder == nil || folder.ID == "" || folder.Path == "" {
			return nil, nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		minimalRoots[folderID] = struct{}{}
		roots = append(roots, interfaces.KnowledgeFolderScopeRoot{
			ID:   folder.ID,
			Path: folder.Path,
		})
	}

	uncoveredDirect := make([]string, 0, len(direct))
	for _, folderID := range direct {
		pathIDs, exists := selectedPaths[folderID]
		if !exists || len(pathIDs) == 0 {
			return nil, nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		covered := false
		for _, pathID := range pathIDs {
			if _, recursiveAncestor := minimalRoots[pathID]; recursiveAncestor {
				covered = true
				break
			}
		}
		if !covered {
			uncoveredDirect = append(uncoveredDirect, folderID)
		}
	}
	sort.Strings(uncoveredDirect)
	return uncoveredDirect, roots, minimalRootIDs, nil
}

func indexSelectedKnowledgeScopeFolders(
	sourceTenantID uint64,
	knowledgeBaseID string,
	selectedIDs []string,
	rows []*types.KnowledgeFolder,
) (
	map[string]*types.KnowledgeFolder,
	map[string][]string,
	error,
) {
	requested := make(map[string]struct{}, len(selectedIDs))
	for _, folderID := range selectedIDs {
		requested[folderID] = struct{}{}
	}
	byID := make(map[string]*types.KnowledgeFolder, len(rows))
	paths := make(map[string][]string, len(rows))
	for _, folder := range rows {
		if folder == nil {
			return nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, requestedID := requested[folder.ID]; !requestedID {
			return nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, duplicate := byID[folder.ID]; duplicate {
			return nil, nil, ErrKnowledgeFolderDataIntegrity
		}
		if folder.TenantID != sourceTenantID ||
			folder.KnowledgeBaseID != knowledgeBaseID ||
			folder.DeletedAt.Valid {
			return nil, nil, ErrKnowledgeFolderNotFound
		}
		pathIDs, err := parseKnowledgeFolderPath(folder)
		if err != nil {
			return nil, nil, err
		}
		byID[folder.ID] = folder
		paths[folder.ID] = pathIDs
	}
	if len(byID) != len(requested) {
		return nil, nil, ErrKnowledgeFolderNotFound
	}
	return byID, paths, nil
}

func missingKnowledgeScopeAncestorIDs(
	selected map[string]*types.KnowledgeFolder,
	paths map[string][]string,
) []string {
	missing := make(map[string]struct{})
	for _, pathIDs := range paths {
		for _, folderID := range pathIDs {
			if _, loaded := selected[folderID]; !loaded {
				missing[folderID] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(missing))
	for folderID := range missing {
		result = append(result, folderID)
	}
	sort.Strings(result)
	return result
}

func mergeKnowledgeScopeAncestorRows(
	sourceTenantID uint64,
	knowledgeBaseID string,
	selected map[string]*types.KnowledgeFolder,
	expectedIDs []string,
	rows []*types.KnowledgeFolder,
) (map[string]*types.KnowledgeFolder, error) {
	byID := make(map[string]*types.KnowledgeFolder, len(selected)+len(rows))
	for folderID, folder := range selected {
		byID[folderID] = folder
	}
	expected := make(map[string]struct{}, len(expectedIDs))
	for _, folderID := range expectedIDs {
		expected[folderID] = struct{}{}
	}
	loaded := make(map[string]struct{}, len(rows))
	for _, folder := range rows {
		if folder == nil {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, wanted := expected[folder.ID]; !wanted {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, duplicate := loaded[folder.ID]; duplicate {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if folder.TenantID != sourceTenantID ||
			folder.KnowledgeBaseID != knowledgeBaseID ||
			folder.DeletedAt.Valid {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, err := parseKnowledgeFolderPath(folder); err != nil {
			return nil, err
		}
		if _, conflicts := byID[folder.ID]; conflicts {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		loaded[folder.ID] = struct{}{}
		byID[folder.ID] = folder
	}
	if len(loaded) != len(expected) {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	return byID, nil
}

func indexKnowledgeScopeSubtreeCandidates(
	sourceTenantID uint64,
	knowledgeBaseID string,
	rows []*types.KnowledgeFolder,
) (map[string]*types.KnowledgeFolder, error) {
	byID := make(map[string]*types.KnowledgeFolder, len(rows))
	for _, folder := range rows {
		if folder == nil ||
			folder.TenantID != sourceTenantID ||
			folder.KnowledgeBaseID != knowledgeBaseID ||
			folder.DeletedAt.Valid {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, duplicate := byID[folder.ID]; duplicate {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, err := parseKnowledgeFolderPath(folder); err != nil {
			return nil, err
		}
		byID[folder.ID] = folder
	}
	return byID, nil
}

func validateKnowledgeScopeSubtree(
	rootIDs []string,
	selected map[string]*types.KnowledgeFolder,
	candidates map[string]*types.KnowledgeFolder,
) error {
	roots := make([]*types.KnowledgeFolder, 0, len(rootIDs))
	for _, rootID := range rootIDs {
		root, selectedRoot := selected[rootID]
		candidateRoot, candidateExists := candidates[rootID]
		if !selectedRoot || !candidateExists ||
			!sameKnowledgeScopeFolderStructure(root, candidateRoot) {
			return ErrKnowledgeFolderDataIntegrity
		}
		roots = append(roots, root)
	}

	if knowledgeScopeCandidateCycle(candidates) {
		return ErrKnowledgeFolderDataIntegrity
	}
	pathMembers := make(map[string]struct{}, len(candidates))
	for folderID, candidate := range candidates {
		for _, root := range roots {
			if strings.HasPrefix(candidate.Path, root.Path) {
				pathMembers[folderID] = struct{}{}
				break
			}
		}
	}

	children := make(map[string][]string, len(candidates))
	for folderID, candidate := range candidates {
		children[candidate.ParentID] = append(children[candidate.ParentID], folderID)
	}
	reachable := make(map[string]struct{}, len(candidates))
	queue := append([]string(nil), rootIDs...)
	for len(queue) > 0 {
		folderID := queue[0]
		queue = queue[1:]
		if _, visited := reachable[folderID]; visited {
			continue
		}
		if _, exists := candidates[folderID]; !exists {
			return ErrKnowledgeFolderDataIntegrity
		}
		reachable[folderID] = struct{}{}
		queue = append(queue, children[folderID]...)
	}
	if len(reachable) != len(candidates) ||
		len(pathMembers) != len(candidates) {
		return ErrKnowledgeFolderDataIntegrity
	}
	for folderID := range candidates {
		if _, graphMember := reachable[folderID]; !graphMember {
			return ErrKnowledgeFolderDataIntegrity
		}
		if _, pathMember := pathMembers[folderID]; !pathMember {
			return ErrKnowledgeFolderDataIntegrity
		}
	}
	return nil
}

func knowledgeScopeCandidateCycle(
	candidates map[string]*types.KnowledgeFolder,
) bool {
	const (
		knowledgeScopeUnvisited = iota
		knowledgeScopeVisiting
		knowledgeScopeVisited
	)
	state := make(map[string]int, len(candidates))
	var visit func(string) bool
	visit = func(folderID string) bool {
		switch state[folderID] {
		case knowledgeScopeVisiting:
			return true
		case knowledgeScopeVisited:
			return false
		}
		state[folderID] = knowledgeScopeVisiting
		parentID := candidates[folderID].ParentID
		if _, parentInCandidates := candidates[parentID]; parentInCandidates &&
			visit(parentID) {
			return true
		}
		state[folderID] = knowledgeScopeVisited
		return false
	}
	for folderID := range candidates {
		if visit(folderID) {
			return true
		}
	}
	return false
}

func mergeKnowledgeScopeCandidateRows(
	all map[string]*types.KnowledgeFolder,
	candidates map[string]*types.KnowledgeFolder,
) error {
	for folderID, candidate := range candidates {
		if existing, loaded := all[folderID]; loaded {
			if !sameKnowledgeScopeFolderStructure(existing, candidate) {
				return ErrKnowledgeFolderDataIntegrity
			}
			continue
		}
		all[folderID] = candidate
	}
	return nil
}

func validateKnowledgeScopeFolderChains(
	byID map[string]*types.KnowledgeFolder,
	folderIDs []string,
) error {
	for _, folderID := range folderIDs {
		folder, exists := byID[folderID]
		if !exists || folder == nil {
			return ErrKnowledgeFolderDataIntegrity
		}
		pathIDs, err := parseKnowledgeFolderPath(folder)
		if err != nil {
			return err
		}
		expectedPath := ""
		for index, pathID := range pathIDs {
			pathFolder, pathExists := byID[pathID]
			if !pathExists || pathFolder == nil {
				return ErrKnowledgeFolderDataIntegrity
			}
			expectedParentID := types.KnowledgeFolderRootID
			if index > 0 {
				expectedParentID = pathIDs[index-1]
			}
			expectedPath = knowledgeFolderChildPath(expectedPath, pathID)
			if pathFolder.ParentID != expectedParentID ||
				pathFolder.Depth != index+1 ||
				pathFolder.Path != expectedPath {
				return ErrKnowledgeFolderDataIntegrity
			}
		}
	}
	return nil
}

func sameKnowledgeScopeFolderStructure(
	left *types.KnowledgeFolder,
	right *types.KnowledgeFolder,
) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID &&
		left.TenantID == right.TenantID &&
		left.KnowledgeBaseID == right.KnowledgeBaseID &&
		left.ParentID == right.ParentID &&
		left.Path == right.Path &&
		left.Depth == right.Depth &&
		left.DeletedAt.Valid == right.DeletedAt.Valid
}

func stableUniqueKnowledgeScopeIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func mapKnowledgeScopeRepositoryError(ctx context.Context, err error) error {
	err = preserveKnowledgeScopeContextError(ctx, err)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrKnowledgeFolderNotFound),
		errors.Is(err, ErrKnowledgeFolderDataIntegrity),
		errors.Is(err, ErrKnowledgeFolderUnsupportedDB),
		errors.Is(err, types.ErrInvalidKnowledgeScopeRequest):
		return err
	case errors.Is(err, repository.ErrKnowledgeFolderNotFound):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderNotFound, err)
	case errors.Is(err, repository.ErrKnowledgeFolderDataIntegrity):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderDataIntegrity, err)
	case errors.Is(err, repository.ErrKnowledgeFolderUnsupportedDialect):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderUnsupportedDB, err)
	default:
		return err
	}
}

func preserveKnowledgeScopeContextError(
	ctx context.Context,
	err error,
) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

func isNilKnowledgeScopeDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Ptr,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
