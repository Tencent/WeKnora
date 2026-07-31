package handler

import (
	"context"
	goerrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// resolveAuthorizedKnowledgeSearchScopes is the single authorization path for
// both file and folder mention search. Returning explicit (tenant, KB) pairs
// keeps the two result types from drifting into different visibility rules.
func (h *KnowledgeHandler) resolveAuthorizedKnowledgeSearchScopes(
	c *gin.Context,
	ctx context.Context,
	agentID string,
) ([]types.KnowledgeSearchScope, error) {
	if agentID != "" {
		return h.resolveSharedAgentKnowledgeSearchScopes(c, ctx, agentID)
	}

	if scopes, restricted := tenantAPIKeySearchScopes(ctx); restricted {
		return scopes, nil
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		return nil, errors.NewUnauthorizedError("workspace ID not found")
	}

	ownKBs, err := h.kbService.ListKnowledgeBases(ctx)
	scopes := make([]types.KnowledgeSearchScope, 0, len(ownKBs))
	if err == nil {
		for _, kb := range ownKBs {
			if kb != nil && kb.Type == types.KnowledgeBaseTypeDocument {
				scopes = append(scopes, types.KnowledgeSearchScope{TenantID: tenantID, KBID: kb.ID})
			}
		}
	}

	if userIDVal, ok := c.Get(types.UserIDContextKey.String()); ok && h.kbShareService != nil {
		if userID, ok := userIDVal.(string); ok && userID != "" {
			callerTenantRole := types.TenantRoleFromContext(ctx)
			shared, err := h.kbShareService.ListSharedKnowledgeBases(ctx, tenantID, callerTenantRole)
			if err == nil {
				for _, item := range shared {
					if item == nil || item.KnowledgeBase == nil ||
						item.KnowledgeBase.Type != types.KnowledgeBaseTypeDocument {
						continue
					}
					scopes = append(scopes, types.KnowledgeSearchScope{
						TenantID: item.SourceTenantID,
						KBID:     item.KnowledgeBase.ID,
					})
				}
			}
		}
	}

	return filterKnowledgeSearchScopesForAPIKey(ctx, scopes), nil
}

func (h *KnowledgeHandler) resolveSharedAgentKnowledgeSearchScopes(
	c *gin.Context,
	ctx context.Context,
	agentID string,
) ([]types.KnowledgeSearchScope, error) {
	if _, ok := c.Get(types.UserIDContextKey.String()); !ok {
		return nil, errors.NewUnauthorizedError("user ID not found")
	}
	currentTenantID := c.GetUint64(types.TenantIDContextKey.String())
	if currentTenantID == 0 {
		return nil, errors.NewUnauthorizedError("workspace ID not found")
	}
	if h.agentShareService == nil {
		return nil, errors.NewInternalServerError("shared agent service is unavailable")
	}

	callerTenantRole := types.TenantRoleFromContext(ctx)
	requestedSourceTenantID, parseErr := types.ParseAgentSourceTenantID(
		c.Query(types.AgentSourceTenantIDParam),
	)
	if parseErr != nil {
		return nil, errors.NewBadRequestError(parseErr.Error())
	}
	agent, err := h.agentShareService.GetSharedAgentForTenant(
		ctx,
		currentTenantID,
		callerTenantRole,
		agentID,
		requestedSourceTenantID,
	)
	if err != nil {
		if goerrors.Is(err, service.ErrAgentShareNotFound) ||
			goerrors.Is(err, service.ErrAgentSharePermission) ||
			goerrors.Is(err, service.ErrAgentNotFoundForShare) {
			return nil, errors.NewForbiddenError("no permission for this shared agent")
		}
		logger.ErrorWithFields(ctx, err, nil)
		return nil, errors.NewInternalServerError("Failed to verify shared agent access").WithDetails(err.Error())
	}

	sourceTenantID := agent.TenantID
	mode := agent.Config.KBSelectionMode
	if mode == "none" {
		return nil, nil
	}
	if mode == "selected" && len(agent.Config.KnowledgeBases) > 0 {
		scopes := make([]types.KnowledgeSearchScope, 0, len(agent.Config.KnowledgeBases))
		for _, kbID := range agent.Config.KnowledgeBases {
			// Preserve main's legacy Agent semantics exactly. Normalizing stored
			// IDs belongs to a separate compatibility change, not folder search.
			if kbID != "" {
				scopes = append(scopes, types.KnowledgeSearchScope{
					TenantID: sourceTenantID,
					KBID:     kbID,
				})
			}
		}
		return filterKnowledgeSearchScopesForAPIKey(ctx, scopes), nil
	}

	var scopes []types.KnowledgeSearchScope
	kbs, err := h.kbService.ListKnowledgeBasesByTenantID(ctx, sourceTenantID)
	if err != nil {
		return nil, errors.NewInternalServerError("Failed to list knowledge bases").WithDetails(err.Error())
	}
	filter := tools.DeriveKBFilterForAgent(agent.Config.AgentMode, agent.Config.AllowedTools)
	for _, kb := range kbs {
		if kb == nil || kb.Type != types.KnowledgeBaseTypeDocument {
			continue
		}
		if !filter.IsEmpty() &&
			!tools.KBSatisfiesAgentRequirements(
				kb.Capabilities(),
				agent.Config.AgentMode,
				agent.Config.AllowedTools,
			) {
			continue
		}
		scopes = append(scopes, types.KnowledgeSearchScope{
			TenantID: sourceTenantID,
			KBID:     kb.ID,
		})
	}

	return filterKnowledgeSearchScopesForAPIKey(ctx, scopes), nil
}

// restrictKnowledgeSearchScopes applies a caller-requested KB subset only
// after authorization has produced the maximum visible scope. It can narrow a
// search (used by an agent configured with selected KBs) but can never widen
// it.
func restrictKnowledgeSearchScopes(
	scopes []types.KnowledgeSearchScope,
	rawKBIDs string,
) []types.KnowledgeSearchScope {
	rawKBIDs = strings.TrimSpace(rawKBIDs)
	if rawKBIDs == "" {
		return scopes
	}
	allowed := make(map[string]struct{})
	for _, kbID := range strings.Split(rawKBIDs, ",") {
		if kbID = strings.TrimSpace(kbID); kbID != "" {
			allowed[kbID] = struct{}{}
		}
	}
	out := make([]types.KnowledgeSearchScope, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := allowed[scope.KBID]; ok {
			out = append(out, scope)
		}
	}
	return out
}

func (h *KnowledgeHandler) validateFolderForKnowledgeCreate(
	ctx context.Context,
	kb *types.KnowledgeBase,
	folderID string,
) error {
	if folderID == "" {
		return nil
	}
	if kb == nil || kb.Type != types.KnowledgeBaseTypeDocument || kb.IsWikiEnabled() {
		return errors.NewBadRequestError(
			"Document folders are not supported for this knowledge base",
		)
	}
	if !config.DocumentFoldersEnabled(h.cfg) {
		return errors.NewServiceUnavailableError(
			"document folders are disabled until the rolling upgrade is complete",
		)
	}
	if h.folderService == nil {
		return errors.NewInternalServerError("document folder service is unavailable")
	}
	if err := h.folderService.ValidateFolderExistsForUpload(ctx, kb.ID, folderID); err != nil {
		if goerrors.Is(err, repository.ErrDocumentFolderNotFound) {
			return errors.NewBadRequestError("target folder does not exist")
		}
		return errors.NewInternalServerError("failed to validate target folder").WithDetails(err.Error())
	}
	return nil
}

func (h *KnowledgeHandler) filterDocumentFolderSearchScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
) ([]types.KnowledgeSearchScope, error) {
	if len(scopes) == 0 {
		return nil, nil
	}

	kbIDs := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, ok := seen[scope.KBID]; ok {
			continue
		}
		seen[scope.KBID] = struct{}{}
		kbIDs = append(kbIDs, scope.KBID)
	}

	kbs, err := h.kbService.GetKnowledgeBasesByIDsOnly(ctx, kbIDs)
	if err != nil {
		return nil, err
	}
	allowed := make(map[types.KnowledgeSearchScope]struct{}, len(kbs))
	for _, kb := range kbs {
		if kb == nil || kb.Type != types.KnowledgeBaseTypeDocument || kb.IsWikiEnabled() {
			continue
		}
		allowed[types.KnowledgeSearchScope{TenantID: kb.TenantID, KBID: kb.ID}] = struct{}{}
	}

	filtered := make([]types.KnowledgeSearchScope, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := allowed[scope]; ok {
			filtered = append(filtered, scope)
		}
	}
	return filtered, nil
}

// handleFolderPlacementWriteError maps the final transactional folder check.
// The normal preflight catches stale IDs early; this branch covers the narrow
// race where the folder or KB is deleted between preflight and persistence.
func handleFolderPlacementWriteError(c *gin.Context, err error) bool {
	switch {
	case goerrors.Is(err, repository.ErrDocumentFolderNotFound):
		c.Error(errors.NewBadRequestError("target folder does not exist"))
		return true
	case goerrors.Is(err, repository.ErrKnowledgeBaseNotFound):
		c.Error(errors.NewNotFoundError("knowledge base not found"))
		return true
	default:
		return false
	}
}

// SearchDocumentFolders returns typed folder results for the same authorized
// KB scopes as SearchKnowledge. The frontend calls both endpoints in parallel
// while the user types after @.
func (h *KnowledgeHandler) SearchDocumentFolders(c *gin.Context) {
	if !config.DocumentFoldersEnabled(h.cfg) {
		c.Error(errors.NewServiceUnavailableError(
			"document folders are disabled until the rolling upgrade is complete",
		))
		return
	}
	ctx := c.Request.Context()
	if userID, ok := c.Get(types.UserIDContextKey.String()); ok {
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
	}

	keyword := strings.TrimSpace(c.Query("keyword"))
	if keyword == "" {
		keyword = strings.TrimSpace(c.Query("query"))
	}
	if keyword == "" {
		c.Error(errors.NewBadRequestError("missing search keyword: pass ?keyword=... or ?query=..."))
		return
	}

	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		c.Error(errors.NewBadRequestError("offset must be a non-negative integer"))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		c.Error(errors.NewBadRequestError("limit must be between 1 and 100"))
		return
	}
	if h.folderService == nil {
		c.Error(errors.NewInternalServerError("document folder service is unavailable"))
		return
	}

	scopes, err := h.resolveAuthorizedKnowledgeSearchScopes(c, ctx, c.Query("agent_id"))
	if err != nil {
		c.Error(err)
		return
	}
	scopes = restrictKnowledgeSearchScopes(scopes, c.Query("kb_ids"))
	scopes, err = h.filterDocumentFolderSearchScopes(ctx, scopes)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to validate folder search scopes").WithDetails(err.Error()))
		return
	}
	if len(scopes) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success":  true,
			"data":     []interface{}{},
			"has_more": false,
			"total":    0,
		})
		return
	}

	folders, hasMore, total, err := h.folderService.SearchFolders(
		ctx,
		scopes,
		keyword,
		offset,
		limit,
	)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to search folders").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     folders,
		"has_more": hasMore,
		"total":    total,
	})
}
