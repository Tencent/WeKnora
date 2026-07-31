package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"slices"
	"sort"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	knowledgeScopeUnavailableMessage = "folder-scoped retrieval is temporarily unavailable"

	// This page size is only for ID materialization pagination, not Retriever batching.
	knowledgeScopeMaterializationPageSize = 1000
	// This conservative guardrail independently caps each target's candidate SQL
	// predicate and the request-wide materialized scope; it is not a shared counter
	// and should be benchmarked before adjustment.
	knowledgeScopeMaxMaterializedKnowledgeIDs = 10000
)

func knowledgeScopeHashPrefix(hash string) string {
	const prefixLength = 12
	if len(hash) <= prefixLength {
		return hash
	}
	return hash[:prefixLength]
}

// PrepareKnowledgeScope reconciles, authorizes, resolves, and hashes one
// ordinary HTTP QA/Search scope without trusting client identity metadata.
func (s *sessionService) PrepareKnowledgeScope(
	ctx context.Context,
	input types.KnowledgeScopePrepareInput,
) (*types.KnowledgeScopePreparation, error) {
	if ctx == nil {
		return nil, apperrors.NewBadRequestError("invalid knowledge scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	callerTenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || callerTenantID == 0 {
		return nil, apperrors.NewForbiddenError("knowledge scope is not authorized")
	}
	if _, ok = types.PrincipalFromContext(ctx); !ok {
		return nil, apperrors.NewForbiddenError("knowledge scope is not authorized")
	}
	if s == nil || s.knowledgeScopeAuthRepo == nil ||
		s.knowledgeScopeResolver == nil ||
		s.knowledgeBaseService == nil ||
		s.knowledgeService == nil {
		return nil, apperrors.NewInternalServerError("knowledge scope preparation failed")
	}

	var err error
	legacy := input.LegacyRequest.Clone()
	if input.CanonicalRequest == nil && !knowledgeScopeRequestHasSelectors(legacy) {
		legacy, err = s.defaultKnowledgeScopeRequest(ctx, input)
		if err != nil {
			return nil, mapKnowledgeScopePreparationError(ctx, err)
		}
	}
	if !knowledgeScopeRequestHasSelectors(legacy) {
		legacy = nil
	}

	canonical := input.CanonicalRequest.Clone()
	request,
		preboundKnowledgeByID,
		preboundKnowledgeIDsByKB,
		err := s.reconcileKnowledgeScopeRequests(
		ctx,
		canonical,
		legacy,
	)
	if err != nil {
		return nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	if request == nil {
		request = &types.KnowledgeScopeRequest{}
	}
	if err = s.knowledgeScopeResolver.ValidateFolderSelectorBudget(
		request,
	); err != nil {
		return nil, mapKnowledgeScopePreparationError(ctx, err)
	}

	knowledgeByID := preboundKnowledgeByID
	knowledgeIDsByKB := preboundKnowledgeIDsByKB
	if knowledgeByID == nil {
		knowledgeByID, knowledgeIDsByKB, err =
			s.resolveKnowledgeScopeKnowledge(
				ctx,
				request.KnowledgeIDs,
			)
		if err != nil {
			return nil, err
		}
	}
	tagIDsByKB, tagByID, err := s.resolveKnowledgeScopeTags(
		ctx,
		request.TagScopes,
	)
	if err != nil {
		return nil, err
	}
	folderByID, err := s.resolveKnowledgeScopeFolders(
		ctx,
		request.FolderScopes,
	)
	if err != nil {
		return nil, err
	}

	targetKBIDs := knowledgeScopeTargetKnowledgeBaseIDs(
		request,
		knowledgeByID,
	)
	knowledgeBases, knowledgeBaseByID, err :=
		s.loadKnowledgeScopeKnowledgeBases(ctx, targetKBIDs)
	if err != nil {
		return nil, err
	}
	if err = validateSharedAgentKnowledgeScopeTargetTenants(
		input,
		knowledgeBases,
	); err != nil {
		return nil, err
	}
	if err = types.AuthorizeTenantAPIKeyKnowledgeBases(
		ctx,
		targetKBIDs...,
	); err != nil {
		return nil, err
	}
	if err = s.authorizeKnowledgeScopeTargets(
		ctx,
		callerTenantID,
		knowledgeBases,
	); err != nil {
		return nil, err
	}
	if err = validateKnowledgeScopeReferenceTenants(
		knowledgeByID,
		tagByID,
		folderByID,
		knowledgeBaseByID,
	); err != nil {
		return nil, err
	}
	if err = s.validateKnowledgeScopeAgentTargets(
		ctx,
		input,
		targetKBIDs,
	); err != nil {
		return nil, err
	}

	authorizedTargets, err := s.buildAuthorizedKnowledgeScopeTargets(
		ctx,
		targetKBIDs,
		knowledgeBaseByID,
		knowledgeIDsByKB,
		tagIDsByKB,
	)
	if err != nil {
		return nil, err
	}
	execution, err := s.knowledgeScopeResolver.Resolve(
		ctx,
		types.KnowledgeScopeResolveInput{
			Request:           request.Clone(),
			AuthorizedTargets: authorizedTargets,
		},
	)
	if err != nil {
		return nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	for _, target := range execution.Targets() {
		if !target.FolderFilter().Enabled() {
			continue
		}
		execution, err = s.materializeKnowledgeScope(
			ctx,
			execution,
			knowledgeScopeMaterializationPageSize,
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		)
		if err != nil {
			return nil, mapKnowledgeScopePreparationError(ctx, err)
		}
		break
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	executionScopeHash, err := types.HashKnowledgeScope(execution)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	preparation, err := types.NewKnowledgeScopePreparation(
		request,
		execution,
		executionScopeHash,
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	return preparation, nil
}

func (s *sessionService) materializeKnowledgeScope(
	ctx context.Context,
	execution *types.KnowledgeScope,
	pageSize int,
	maxKnowledgeIDs int,
) (*types.KnowledgeScope, error) {
	if ctx == nil || execution == nil || s == nil || s.knowledgeService == nil {
		return nil, apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	if pageSize <= 0 || maxKnowledgeIDs <= 0 {
		return nil, apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	targets := execution.Targets()
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folderFilter := target.FolderFilter()
		if !folderFilter.Enabled() || len(folderFilter.FolderIDs()) == 0 {
			continue
		}
		if len(target.KnowledgeIDs()) > maxKnowledgeIDs {
			return nil, fmt.Errorf(
				"%w: knowledge scope candidate limit exceeded",
				types.ErrKnowledgeScopeTooLarge,
			)
		}
	}

	materializedTotal := 0
	rebuiltTargets := make(
		[]types.KnowledgeScopeTarget,
		0,
		len(targets),
	)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		folderFilter := target.FolderFilter()
		knowledgeIDs := target.KnowledgeIDs()
		materializedIDs := knowledgeIDs
		if folderFilter.Enabled() {
			materializedIDs = nil
		}

		folderIDs := folderFilter.FolderIDs()
		knownEmpty := len(target.ScopeTagIDs()) > 0 &&
			len(target.TagIDs()) == 0 &&
			len(knowledgeIDs) == 0
		if folderFilter.Enabled() && len(folderIDs) > 0 && !knownEmpty {
			var candidate []string
			var candidateSet map[string]struct{}
			if len(knowledgeIDs) > 0 {
				candidate = append([]string(nil), knowledgeIDs...)
				candidateSet = make(map[string]struct{}, len(candidate))
				for _, knowledgeID := range candidate {
					candidateSet[knowledgeID] = struct{}{}
				}
			}

			cursor := ""
			seen := make(map[string]struct{})
			for {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				remaining := maxKnowledgeIDs - materializedTotal
				callLimit := pageSize
				if remaining < pageSize {
					callLimit = remaining + 1
				}
				page, hasMore, err :=
					s.knowledgeService.ListActiveKnowledgeIDsByFolderIDs(
						ctx,
						target.SourceTenantID(),
						target.KnowledgeBaseID(),
						append([]string(nil), folderIDs...),
						append([]string(nil), candidate...),
						cursor,
						callLimit,
					)
				if err != nil {
					if stderrors.Is(err, context.Canceled) ||
						stderrors.Is(err, context.DeadlineExceeded) {
						return nil, err
					}
					if contextErr := ctx.Err(); contextErr != nil {
						return nil, contextErr
					}
					if stderrors.Is(err, types.ErrInvalidKnowledgeScopeRequest) {
						return nil, apperrors.NewInternalServerError(
							"knowledge scope preparation failed",
						)
					}
					return nil, err
				}
				if err = ctx.Err(); err != nil {
					return nil, err
				}
				if len(page) > callLimit ||
					(hasMore && len(page) != callLimit) {
					return nil, apperrors.NewInternalServerError(
						"knowledge scope preparation failed",
					)
				}

				previousID := cursor
				for _, knowledgeID := range page {
					if knowledgeID == "" || knowledgeID <= previousID {
						return nil, apperrors.NewInternalServerError(
							"knowledge scope preparation failed",
						)
					}
					if _, duplicate := seen[knowledgeID]; duplicate {
						return nil, apperrors.NewInternalServerError(
							"knowledge scope preparation failed",
						)
					}
					if candidateSet != nil {
						if _, allowed := candidateSet[knowledgeID]; !allowed {
							return nil, apperrors.NewInternalServerError(
								"knowledge scope preparation failed",
							)
						}
					}
					previousID = knowledgeID
				}
				if len(page) > remaining ||
					(len(page) == remaining && hasMore) {
					return nil, fmt.Errorf(
						"%w: materialized knowledge scope limit exceeded",
						types.ErrKnowledgeScopeTooLarge,
					)
				}
				for _, knowledgeID := range page {
					seen[knowledgeID] = struct{}{}
				}
				materializedIDs = append(materializedIDs, page...)
				materializedTotal += len(page)
				if !hasMore {
					break
				}
				cursor = page[len(page)-1]
			}
		}

		rebuilt, err := types.NewKnowledgeScopeTarget(
			target.KnowledgeBaseID(),
			target.SourceTenantID(),
			materializedIDs,
			target.TagIDs(),
			target.ScopeTagIDs(),
			folderFilter,
		)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		rebuiltTargets = append(rebuiltTargets, rebuilt)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rebuilt, err := types.NewKnowledgeScope(rebuiltTargets)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	return rebuilt, nil
}

func (s *sessionService) reconcileKnowledgeScopeRequests(
	ctx context.Context,
	canonical *types.KnowledgeScopeRequest,
	legacy *types.KnowledgeScopeRequest,
) (
	*types.KnowledgeScopeRequest,
	map[string]*types.Knowledge,
	map[string][]string,
	error,
) {
	request, err := types.ReconcileKnowledgeScopeRequest(canonical, legacy)
	if err != nil {
		return nil, nil, nil, err
	}
	if canonical == nil || legacy == nil {
		return request, nil, nil, nil
	}

	normalizedLegacy, err := types.NormalizeKnowledgeScopeRequest(legacy)
	if err != nil {
		return nil, nil, nil, err
	}
	knowledgeByID, knowledgeIDsByKB, err :=
		s.resolveKnowledgeScopeKnowledge(
			ctx,
			request.KnowledgeIDs,
		)
	if err != nil {
		return nil, nil, nil, err
	}
	canonicalKBIDs := knowledgeScopeTargetKnowledgeBaseIDs(
		request,
		knowledgeByID,
	)
	legacyKBIDs := knowledgeScopeTargetKnowledgeBaseIDs(
		normalizedLegacy,
		knowledgeByID,
	)
	if !slices.Equal(canonicalKBIDs, legacyKBIDs) {
		return nil, nil, nil, invalidKnowledgeScopeConflict()
	}
	return request, knowledgeByID, knowledgeIDsByKB, nil
}

func invalidKnowledgeScopeConflict() error {
	return fmt.Errorf(
		"%w: canonical and legacy scope differ",
		types.ErrInvalidKnowledgeScopeRequest,
	)
}

func knowledgeScopeRequestHasSelectors(
	request *types.KnowledgeScopeRequest,
) bool {
	if request == nil {
		return false
	}
	return len(request.KnowledgeBaseIDs) > 0 ||
		len(request.KnowledgeIDs) > 0 ||
		len(request.TagScopes) > 0 ||
		request.FolderScopes != nil
}

func (s *sessionService) defaultKnowledgeScopeRequest(
	ctx context.Context,
	input types.KnowledgeScopePrepareInput,
) (*types.KnowledgeScopeRequest, error) {
	agent := input.CustomAgent
	if agent == nil || agent.Config.RetrieveKBOnlyWhenMentioned {
		return &types.KnowledgeScopeRequest{}, nil
	}
	callerTenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || callerTenantID == 0 {
		return nil, stderrors.New("caller tenant is unavailable")
	}
	effectiveAgent := *agent
	if effectiveAgent.TenantID == 0 {
		if input.SharedAgent {
			return nil, stderrors.New("agent tenant is unavailable")
		}
		effectiveAgent.TenantID = callerTenantID
	}
	sessionTenantID := effectiveAgent.TenantID
	if input.SharedAgent && input.Session != nil {
		sessionTenantID = input.Session.TenantID
	}
	if input.SharedAgent && sessionTenantID == 0 {
		sessionTenantID = callerTenantID
	}
	agentContext := context.WithValue(
		ctx,
		types.TenantIDContextKey,
		effectiveAgent.TenantID,
	)
	knowledgeBaseIDs, err := s.resolveKnowledgeBasesFromAgentStrict(
		agentContext,
		&effectiveAgent,
		sessionTenantID,
	)
	if err != nil {
		return nil, err
	}
	return &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: knowledgeBaseIDs,
	}, nil
}

func (s *sessionService) resolveKnowledgeScopeKnowledge(
	ctx context.Context,
	knowledgeIDs []string,
) (
	map[string]*types.Knowledge,
	map[string][]string,
	error,
) {
	byID := make(map[string]*types.Knowledge, len(knowledgeIDs))
	byKB := make(map[string][]string)
	if len(knowledgeIDs) == 0 {
		return byID, byKB, nil
	}
	rows, err := s.knowledgeScopeAuthRepo.ListKnowledgeScopeReferencesByIDs(
		ctx,
		knowledgeIDs,
	)
	if err != nil {
		return nil, nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	for _, knowledge := range rows {
		if knowledge == nil || knowledge.ID == "" ||
			knowledge.KnowledgeBaseID == "" {
			return nil, nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		if _, duplicate := byID[knowledge.ID]; duplicate {
			return nil, nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		byID[knowledge.ID] = knowledge
		byKB[knowledge.KnowledgeBaseID] = append(
			byKB[knowledge.KnowledgeBaseID],
			knowledge.ID,
		)
	}
	if len(byID) != len(knowledgeIDs) {
		return nil, nil, apperrors.NewNotFoundError("knowledge scope not found")
	}
	for _, id := range knowledgeIDs {
		if byID[id] == nil {
			return nil, nil, apperrors.NewNotFoundError(
				"knowledge scope not found",
			)
		}
	}
	return byID, byKB, nil
}

func (s *sessionService) resolveKnowledgeScopeTags(
	ctx context.Context,
	scopes []types.TagScope,
) (
	map[string][]string,
	map[string]*types.KnowledgeTag,
	error,
) {
	tagIDsByKB := mergeTagScopesByKB(scopes)
	allTagIDs := make([]string, 0)
	expectedKBByTagID := make(map[string]string)
	for kbID, tagIDs := range tagIDsByKB {
		for _, tagID := range tagIDs {
			if expected, exists := expectedKBByTagID[tagID]; exists &&
				expected != kbID {
				return nil, nil, apperrors.NewNotFoundError(
					"knowledge scope not found",
				)
			}
			expectedKBByTagID[tagID] = kbID
			allTagIDs = append(allTagIDs, tagID)
		}
	}
	if len(allTagIDs) == 0 {
		return tagIDsByKB, map[string]*types.KnowledgeTag{}, nil
	}
	rows, err := s.knowledgeScopeAuthRepo.ListKnowledgeTagScopeReferencesByIDs(
		ctx,
		allTagIDs,
	)
	if err != nil {
		return nil, nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	found := make(map[string]*types.KnowledgeTag, len(rows))
	for _, tag := range rows {
		if tag == nil || tag.ID == "" ||
			tag.KnowledgeBaseID != expectedKBByTagID[tag.ID] {
			return nil, nil, apperrors.NewNotFoundError(
				"knowledge scope not found",
			)
		}
		if _, duplicate := found[tag.ID]; duplicate {
			return nil, nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		found[tag.ID] = tag
	}
	if len(found) != len(expectedKBByTagID) {
		return nil, nil, apperrors.NewNotFoundError(
			"knowledge scope not found",
		)
	}
	return tagIDsByKB, found, nil
}

func (s *sessionService) resolveKnowledgeScopeFolders(
	ctx context.Context,
	scopes *[]types.FolderScopeRequest,
) (map[string]*types.KnowledgeFolder, error) {
	expectedKBByFolderID := make(map[string]string)
	if scopes == nil {
		return map[string]*types.KnowledgeFolder{}, nil
	}
	for _, scope := range *scopes {
		for _, folderID := range scope.FolderIDs {
			if folderID == types.KnowledgeFolderRootID {
				continue
			}
			if expected, exists := expectedKBByFolderID[folderID]; exists &&
				expected != scope.KnowledgeBaseID {
				return nil, apperrors.NewNotFoundError(
					"knowledge scope not found",
				)
			}
			expectedKBByFolderID[folderID] = scope.KnowledgeBaseID
		}
	}
	if len(expectedKBByFolderID) == 0 {
		return map[string]*types.KnowledgeFolder{}, nil
	}

	folderIDs := make([]string, 0, len(expectedKBByFolderID))
	for folderID := range expectedKBByFolderID {
		folderIDs = append(folderIDs, folderID)
	}
	sort.Strings(folderIDs)
	rows, err := s.knowledgeScopeAuthRepo.
		ListKnowledgeFolderScopeReferencesByIDs(ctx, folderIDs)
	if err != nil {
		return nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	found := make(map[string]*types.KnowledgeFolder, len(rows))
	for _, folder := range rows {
		if folder == nil || folder.ID == "" ||
			folder.KnowledgeBaseID != expectedKBByFolderID[folder.ID] {
			return nil, apperrors.NewNotFoundError(
				"knowledge scope not found",
			)
		}
		if _, duplicate := found[folder.ID]; duplicate {
			return nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		found[folder.ID] = folder
	}
	if len(found) != len(expectedKBByFolderID) {
		return nil, apperrors.NewNotFoundError("knowledge scope not found")
	}
	return found, nil
}

func knowledgeScopeTargetKnowledgeBaseIDs(
	request *types.KnowledgeScopeRequest,
	knowledgeByID map[string]*types.Knowledge,
) []string {
	seen := make(map[string]struct{})
	for _, id := range request.KnowledgeBaseIDs {
		seen[id] = struct{}{}
	}
	for _, knowledge := range knowledgeByID {
		seen[knowledge.KnowledgeBaseID] = struct{}{}
	}
	for _, scope := range request.TagScopes {
		seen[scope.KnowledgeBaseID] = struct{}{}
	}
	if request.FolderScopes != nil {
		for _, scope := range *request.FolderScopes {
			seen[scope.KnowledgeBaseID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (s *sessionService) loadKnowledgeScopeKnowledgeBases(
	ctx context.Context,
	knowledgeBaseIDs []string,
) (
	[]*types.KnowledgeBase,
	map[string]*types.KnowledgeBase,
	error,
) {
	byID := make(map[string]*types.KnowledgeBase, len(knowledgeBaseIDs))
	if len(knowledgeBaseIDs) == 0 {
		return []*types.KnowledgeBase{}, byID, nil
	}
	rows, err := s.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(
		ctx,
		knowledgeBaseIDs,
	)
	if err != nil {
		return nil, nil, mapKnowledgeScopePreparationError(ctx, err)
	}
	for _, knowledgeBase := range rows {
		if knowledgeBase == nil || knowledgeBase.ID == "" {
			return nil, nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		if _, duplicate := byID[knowledgeBase.ID]; duplicate {
			return nil, nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		byID[knowledgeBase.ID] = knowledgeBase
	}
	if len(byID) != len(knowledgeBaseIDs) {
		return nil, nil, apperrors.NewNotFoundError("knowledge scope not found")
	}
	ordered := make([]*types.KnowledgeBase, 0, len(knowledgeBaseIDs))
	for _, id := range knowledgeBaseIDs {
		knowledgeBase := byID[id]
		if knowledgeBase == nil {
			return nil, nil, apperrors.NewNotFoundError(
				"knowledge scope not found",
			)
		}
		ordered = append(ordered, knowledgeBase)
	}
	return ordered, byID, nil
}

func (s *sessionService) authorizeKnowledgeScopeTargets(
	ctx context.Context,
	callerTenantID uint64,
	knowledgeBases []*types.KnowledgeBase,
) error {
	callerRole := types.TenantRoleFromContext(ctx)
	for _, knowledgeBase := range knowledgeBases {
		if err := ctx.Err(); err != nil {
			return err
		}
		if knowledgeBase.TenantID == callerTenantID {
			continue
		}
		if s.kbShareService == nil {
			return apperrors.NewForbiddenError("knowledge scope is not authorized")
		}
		allowed, err := s.kbShareService.HasTenantKBPermission(
			ctx,
			knowledgeBase.ID,
			callerTenantID,
			callerRole,
			types.OrgRoleViewer,
		)
		if err != nil {
			return mapKnowledgeScopePreparationError(ctx, err)
		}
		if !allowed {
			return apperrors.NewForbiddenError("knowledge scope is not authorized")
		}
	}
	return nil
}

func validateSharedAgentKnowledgeScopeTargetTenants(
	input types.KnowledgeScopePrepareInput,
	knowledgeBases []*types.KnowledgeBase,
) error {
	agent := input.CustomAgent
	if !input.SharedAgent || len(knowledgeBases) == 0 {
		return nil
	}
	if agent == nil || agent.TenantID == 0 {
		return apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	for _, knowledgeBase := range knowledgeBases {
		if knowledgeBase == nil {
			return apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		if knowledgeBase.TenantID != agent.TenantID {
			return apperrors.NewForbiddenError(
				"knowledge scope is not authorized",
			)
		}
	}
	return nil
}

func validateKnowledgeScopeReferenceTenants(
	knowledgeByID map[string]*types.Knowledge,
	tagByID map[string]*types.KnowledgeTag,
	folderByID map[string]*types.KnowledgeFolder,
	knowledgeBaseByID map[string]*types.KnowledgeBase,
) error {
	for _, knowledge := range knowledgeByID {
		knowledgeBase := knowledgeBaseByID[knowledge.KnowledgeBaseID]
		if knowledgeBase == nil || knowledge.TenantID != knowledgeBase.TenantID {
			return apperrors.NewNotFoundError("knowledge scope not found")
		}
	}
	for _, tag := range tagByID {
		knowledgeBase := knowledgeBaseByID[tag.KnowledgeBaseID]
		if knowledgeBase == nil || tag.TenantID != knowledgeBase.TenantID {
			return apperrors.NewNotFoundError("knowledge scope not found")
		}
	}
	for _, folder := range folderByID {
		knowledgeBase := knowledgeBaseByID[folder.KnowledgeBaseID]
		if knowledgeBase == nil || folder.TenantID != knowledgeBase.TenantID {
			return apperrors.NewNotFoundError("knowledge scope not found")
		}
	}
	return nil
}

func (s *sessionService) validateKnowledgeScopeAgentTargets(
	ctx context.Context,
	input types.KnowledgeScopePrepareInput,
	targetKBIDs []string,
) error {
	agent := input.CustomAgent
	if !input.SharedAgent || len(targetKBIDs) == 0 {
		return nil
	}
	if agent == nil || agent.TenantID == 0 {
		return apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	sessionTenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || sessionTenantID == 0 {
		return apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
	if input.Session != nil && input.Session.TenantID != 0 {
		sessionTenantID = input.Session.TenantID
	}
	allowedIDs, err := s.resolveKnowledgeBasesFromAgentStrict(
		context.WithValue(ctx, types.TenantIDContextKey, agent.TenantID),
		agent,
		sessionTenantID,
	)
	if err != nil {
		return mapKnowledgeScopePreparationError(ctx, err)
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range targetKBIDs {
		if _, ok := allowed[id]; !ok {
			return apperrors.NewForbiddenError("knowledge scope is not authorized")
		}
	}
	return nil
}

func (s *sessionService) buildAuthorizedKnowledgeScopeTargets(
	ctx context.Context,
	targetKBIDs []string,
	knowledgeBaseByID map[string]*types.KnowledgeBase,
	knowledgeIDsByKB map[string][]string,
	tagIDsByKB map[string][]string,
) ([]types.AuthorizedKnowledgeScopeTarget, error) {
	targets := make(
		[]types.AuthorizedKnowledgeScopeTarget,
		0,
		len(targetKBIDs),
	)
	for _, knowledgeBaseID := range targetKBIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		knowledgeBase := knowledgeBaseByID[knowledgeBaseID]
		if knowledgeBase == nil {
			return nil, apperrors.NewInternalServerError(
				"knowledge scope preparation failed",
			)
		}
		knowledgeIDs := append(
			[]string(nil),
			knowledgeIDsByKB[knowledgeBaseID]...,
		)
		scopeTagIDs := append([]string(nil), tagIDsByKB[knowledgeBaseID]...)
		tagIDs := append([]string(nil), scopeTagIDs...)
		if len(scopeTagIDs) > 0 &&
			knowledgeBase.Type != types.KnowledgeBaseTypeFAQ {
			resolved, err := s.knowledgeService.ListKnowledgeIDsByTagIDs(
				ctx,
				knowledgeBase.TenantID,
				knowledgeBaseID,
				scopeTagIDs,
			)
			if err != nil {
				return nil, mapKnowledgeScopePreparationError(ctx, err)
			}
			if len(knowledgeIDs) > 0 {
				resolved = intersectStrings(resolved, knowledgeIDs)
			}
			knowledgeIDs = uniqueNonEmptyStrings(resolved)
			tagIDs = nil
		}
		targets = append(targets, types.AuthorizedKnowledgeScopeTarget{
			KnowledgeBaseID: knowledgeBaseID,
			SourceTenantID:  knowledgeBase.TenantID,
			KnowledgeIDs:    knowledgeIDs,
			TagIDs:          tagIDs,
			ScopeTagIDs:     scopeTagIDs,
		})
	}
	return targets, nil
}

func mapKnowledgeScopePreparationError(
	ctx context.Context,
	err error,
) error {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.Canceled) ||
		stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	switch {
	case stderrors.Is(err, types.ErrKnowledgeScopeTooLarge):
		return apperrors.NewKnowledgeScopeTooLargeError(
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		)
	case stderrors.Is(err, types.ErrInvalidKnowledgeScopeRequest):
		return apperrors.NewBadRequestError("invalid knowledge scope")
	case stderrors.Is(err, ErrKnowledgeFolderNotFound):
		return apperrors.NewNotFoundError("knowledge scope not found")
	case stderrors.Is(err, ErrKnowledgeFolderDataIntegrity),
		stderrors.Is(err, ErrKnowledgeFolderUnsupportedDB):
		return apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	default:
		return apperrors.NewInternalServerError(
			"knowledge scope preparation failed",
		)
	}
}
