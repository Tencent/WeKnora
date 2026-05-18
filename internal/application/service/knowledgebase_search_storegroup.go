package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// storeGroup is one fan-out unit of HybridSearch: a set of KB IDs that share
// the same (VectorStore, owning tenant) pair.
//
// Partition key is (VectorStoreID, OwnerTenantID), not VectorStoreID alone,
// because Organization-shared KBs (kb.TenantID != requestTenantID) need
// their own group whose ownership lookup runs against kb.TenantID — the
// store is owned by the source tenant, not the caller.
//
// BaseParams are immutable across iterations and goroutines. TopK is the
// only mutable per-iteration value; paramsWithTopK builds a fresh
// []RetrieveParams per call so no goroutine sees a slice being mutated by
// another. Callers MUST NOT mutate BaseParams after resolveStoreGroups
// returns; doing so would race with the fan-out goroutines.
type storeGroup struct {
	// StoreID is the bound VectorStore UUID, or "" for the env-store group
	// (KBs with VectorStoreID = NULL). Never echo this in user-facing
	// errors; use secutils.SanitizeForLog when emitting in structured logs.
	StoreID string

	// OwnerTenantID is the tenant that owns the KBs and the store for this
	// group. For Organization-shared KBs this differs from the request's
	// tenant; the factory's StoreOwnedBy must be called with this value.
	OwnerTenantID uint64

	// KBIDs are the knowledge base IDs in this group. The caller MUST have
	// authorized the request to access every ID here (the trust boundary
	// is the HTTP handler / session layer, matching the pre-existing
	// chat_pipeline pattern).
	KBIDs []string

	// Engine is the resolved CompositeRetrieveEngine for this group.
	// Reused across iterative FAQ retries — never re-resolved.
	Engine *retriever.CompositeRetrieveEngine

	// BaseParams is the immutable per-group retrieval parameter list.
	// TopK is filled in at retrieve time via paramsWithTopK.
	BaseParams []types.RetrieveParams

	// TopK is the requested over-retrieval count. The iterative FAQ path
	// (knowledgebase_search_faq.go) updates this between calls to
	// retrieveFromStores. Single-shot HybridSearch sets it once.
	TopK int
}

// resolveStoreGroups partitions kbs by (VectorStoreID, KB.TenantID),
// resolves the engine per group via the PR2 factory using the OWNING
// tenant for ownership lookup, and builds the per-store base RetrieveParams
// once. Returns groups in non-deterministic order (caller must not rely on
// iteration order).
//
// The primary KB supplies the embedding model and FAQ type for params; the
// caller MUST invoke validateSameEmbeddingModel first to guarantee a
// single embedding model identity across kbs.
//
// Errors are translated from sentinel to typed AppError so that the
// upstream handler reports a stable error code without leaking storeIDs:
//
//   - retriever.ErrVectorStoreForbidden →
//     apperrors.NewVectorStoreBindingInvalidError (2200)
//   - retriever.ErrVectorStoreNotFound →
//     apperrors.NewVectorStoreUnavailableError (2201)
//   - retriever.ErrTenantInfoMissing →
//     apperrors.NewVectorStoreBindingInvalidError (2200)
//   - any other error → returned with %w wrap (no UUID embedded).
func (s *knowledgeBaseService) resolveStoreGroups(
	ctx context.Context,
	primary *types.KnowledgeBase,
	kbs []*types.KnowledgeBase,
	params types.SearchParams,
	matchCount int,
) ([]*storeGroup, error) {
	type partitionKey struct {
		storeID  string
		tenantID uint64
	}
	buckets := make(map[partitionKey][]string)
	for _, kb := range kbs {
		sid := ""
		if kb.HasVectorStore() {
			sid = *kb.VectorStoreID
		}
		key := partitionKey{storeID: sid, tenantID: kb.TenantID}
		buckets[key] = append(buckets[key], kb.ID)
	}

	groups := make([]*storeGroup, 0, len(buckets))
	for key, ids := range buckets {
		var storeIDPtr *string
		if key.storeID != "" {
			sid := key.storeID
			storeIDPtr = &sid
		}
		engine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, key.tenantID, storeIDPtr)
		if err != nil {
			return nil, classifyFactoryError(ctx, err, key.tenantID, key.storeID)
		}
		baseParams, err := s.buildRetrievalParams(
			ctx, engine, primary, params, ids, matchCount)
		if err != nil {
			return nil, fmt.Errorf("build store-group params: %w", err)
		}
		groups = append(groups, &storeGroup{
			StoreID:       key.storeID,
			OwnerTenantID: key.tenantID,
			KBIDs:         ids,
			Engine:        engine,
			BaseParams:    baseParams,
			TopK:          matchCount,
		})
	}
	return groups, nil
}

// classifyFactoryError translates retriever sentinels into typed AppErrors
// without leaking the store UUID into the user-facing message. The UUID is
// recorded in the structured log only, sanitized via SanitizeForLog to
// defeat log-injection through CR/LF in store IDs.
func classifyFactoryError(
	ctx context.Context, err error, tenantID uint64, storeID string,
) error {
	logger.WarnWithFields(ctx, logger.Fields{
		"tenant_id": tenantID,
		"store_id":  secutils.SanitizeForLog(storeID),
		"reason":    "resolve store engine",
	}, err.Error())
	switch {
	case errors.Is(err, retriever.ErrVectorStoreForbidden):
		return apperrors.NewVectorStoreBindingInvalidError(
			"vector store bound to the knowledge base is not available")
	case errors.Is(err, retriever.ErrVectorStoreNotFound):
		return apperrors.NewVectorStoreUnavailableError(
			"vector store is currently unavailable")
	case errors.Is(err, retriever.ErrTenantInfoMissing):
		return apperrors.NewVectorStoreBindingInvalidError(
			"tenant information missing in context")
	default:
		return err
	}
}

// authorizeKBAccess rejects multi-KB searches whose scope includes a KB
// that the caller is not entitled to read. Same-tenant KBs always pass.
// Foreign-tenant KBs (Organization-shared) must pass an explicit
// tenant-scoped permission check via kbShareService.HasTenantKBPermission,
// applying the 3-D cap (share role + caller's tenant-org role + tenant
// Viewer cap) introduced in Plan 3 of #1303.
//
// Returning NotFound rather than Forbidden avoids leaking the existence
// of unauthorized KB IDs that the caller could not otherwise observe.
// Structured logs record the rejection with the offending kb_id (always
// safe — KB IDs are UUIDs without sensitive content) and the requesting
// tenant for audit.
func (s *knowledgeBaseService) authorizeKBAccess(
	ctx context.Context,
	kbs []*types.KnowledgeBase,
	requestTenantID uint64,
) error {
	if len(kbs) == 0 {
		return nil
	}

	callerTenantRole := types.TenantRoleFromContext(ctx)

	for _, kb := range kbs {
		if kb.TenantID == requestTenantID {
			continue
		}
		hasPermission, permErr := s.kbShareService.HasTenantKBPermission(
			ctx, kb.ID, requestTenantID, callerTenantRole, types.OrgRoleViewer)
		if permErr != nil {
			logger.ErrorWithFields(ctx, permErr, map[string]interface{}{
				"caller_tenant_id": requestTenantID,
				"kb_tenant_id":     kb.TenantID,
				"kb_id":            kb.ID,
				"reason":           "shared-KB permission lookup failed",
			})
			return apperrors.NewInternalServerError("failed to verify knowledge base access")
		}
		if !hasPermission {
			logger.WarnWithFields(ctx, logger.Fields{
				"caller_tenant_id": requestTenantID,
				"kb_tenant_id":     kb.TenantID,
				"kb_id":            kb.ID,
				"reason":           "tenant lacks viewer permission for foreign-tenant KB",
			}, "search scope rejected: unauthorized foreign-tenant KB")
			return apperrors.NewNotFoundError("knowledge base not found")
		}
	}
	return nil
}

// validateSameEmbeddingModel rejects multi-KB searches that span more than
// one resolved embedding-model identity key. Single-KB calls no-op.
//
// Wiki-only / graph-only KBs (empty resolved key) are tolerated: if every
// KB lacks an embedding model, validation passes and HybridSearch returns
// an empty result set via the allBaseParamsEmpty fast path.
//
// Log fields are sanitized via secutils.SanitizeForLog because resolved
// keys are derived from model.Parameters.BaseURL, which is tenant-
// configured and can contain CR/LF or other control characters.
func (s *knowledgeBaseService) validateSameEmbeddingModel(
	ctx context.Context,
	kbs []*types.KnowledgeBase,
) error {
	if len(kbs) <= 1 {
		return nil
	}
	keys := s.ResolveEmbeddingModelKeys(ctx, kbs)
	var seen string
	for _, kb := range kbs {
		k, ok := keys[kb.ID]
		if !ok || k == "" {
			// Wiki-only / graph-only carve-out: KB has no embedding model.
			continue
		}
		if seen == "" {
			seen = k
			continue
		}
		if k != seen {
			logger.WarnWithFields(ctx, logger.Fields{
				"primary_key": secutils.SanitizeForLog(seen),
				"diverging":   secutils.SanitizeForLog(k),
				"kb_id":       kb.ID,
				"kb_count":    len(kbs),
			}, "multi-KB search rejected: embedding models differ")
			return apperrors.NewBadRequestError(
				"selected knowledge bases use different embedding models; " +
					"multi-KB search requires every knowledge base to share a single embedding model")
		}
	}
	return nil
}
