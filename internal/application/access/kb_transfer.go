package access

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// KBTransferOperation selects the source and destination permission contract.
type KBTransferOperation string

// Supported knowledge-base transfer operations.
const (
	KBTransferClone KBTransferOperation = "clone"
	KBTransferMove  KBTransferOperation = "move"
)

type kbTransferGrant struct {
	caller                            types.Caller
	tenant                            uint64
	source, target, taskID, creatorID string
	operation                         KBTransferOperation
	create                            bool
}

// WithKBTransfer admits a pair of resources after the entry point has resolved
// the source and destination grants and applied its creator/role policy. A new
// clone destination must be a server-reserved ID; it is not an existing KB.
func WithKBTransfer(
	ctx context.Context,
	source, target *types.KnowledgeBase,
	operation KBTransferOperation,
	taskID string,
	create bool,
) (context.Context, error) {
	caller := types.CallerFromContext(ctx)
	if taskID == "" {
		return ctx, ErrForbidden
	}
	if err := validateTransferPair(source, target, caller.TenantID, operation, create); err != nil {
		return ctx, err
	}
	if err := transferAPIScope(ctx, source.ID, target.ID, operation, create); err != nil {
		return ctx, err
	}
	required := types.OrgRoleViewer
	if operation == KBTransferMove {
		required = types.OrgRoleEditor
	}
	if !HasKBGrant(ctx, source.ID, source.TenantID, required) ||
		(!create && !HasKBGrant(ctx, target.ID, target.TenantID, types.OrgRoleEditor)) {
		return ctx, ErrForbidden
	}
	return withTransferGrant(ctx, source, target, operation, taskID, create, false), nil
}

// WithKBTransferTask continues an admitted task. Both KBs must first be loaded
// with their persisted owners (or a reserved new clone destination). This scope
// cannot authorize a different pair or upgrade the worker into a tenant user.
func WithKBTransferTask(
	ctx context.Context,
	source, target *types.KnowledgeBase,
	expectedTenant uint64,
	operation KBTransferOperation,
	taskID string,
	create bool,
) (context.Context, error) {
	if taskID == "" {
		return ctx, ErrForbidden
	}
	if err := validateTransferPair(source, target, expectedTenant, operation, create); err != nil {
		return ctx, err
	}
	ctx = types.WithExecutionTenant(ctx, expectedTenant)
	return withTransferGrant(ctx, source, target, operation, taskID, create, true), nil
}

func withTransferGrant(
	ctx context.Context,
	source, target *types.KnowledgeBase,
	operation KBTransferOperation,
	taskID string,
	create, task bool,
) context.Context {
	caller := types.CallerFromContext(ctx)
	grant := kbTransferGrant{
		caller:    caller,
		tenant:    source.TenantID,
		source:    source.ID,
		target:    target.ID,
		taskID:    taskID,
		creatorID: target.CreatorID,
		operation: operation,
		create:    create,
	}
	required := types.OrgRoleViewer
	if operation == KBTransferMove {
		required = types.OrgRoleEditor
	}
	ctx = context.WithValue(ctx, types.KBGrantsContextKey, []kbGrant{
		{caller: caller, kbID: source.ID, tenantID: source.TenantID, permission: required, task: task},
		{caller: caller, kbID: target.ID, tenantID: target.TenantID, permission: types.OrgRoleEditor, task: task},
	})
	return context.WithValue(ctx, types.KBTransferContextKey, grant)
}

// RequireKBTransfer consumes the exact caller, operation and resource-pair grant.
func RequireKBTransfer(ctx context.Context, source, target *types.KnowledgeBase, operation KBTransferOperation) error {
	grant, ok := ctx.Value(types.KBTransferContextKey).(kbTransferGrant)
	if !ok || grant.caller != types.CallerFromContext(ctx) || grant.operation != operation {
		return ErrForbidden
	}
	if err := validateTransferPair(source, target, grant.tenant, operation, grant.create); err != nil {
		return err
	}
	if grant.source != source.ID || grant.target != target.ID {
		return ErrForbidden
	}
	return transferAPIScope(ctx, source.ID, target.ID, operation, grant.create)
}

// CloneDestination exposes only the destination already admitted by the
// request/worker; CopyKnowledgeBase cannot turn an empty ID into ambient write.
func CloneDestination(ctx context.Context, sourceID string) (id string, create bool, creatorID string, err error) {
	g, ok := ctx.Value(types.KBTransferContextKey).(kbTransferGrant)
	if !ok || g.caller != types.CallerFromContext(ctx) || g.operation != KBTransferClone || g.source != sourceID {
		return "", false, "", ErrForbidden
	}
	return g.target, g.create, g.creatorID, nil
}

// TransferTaskID returns the admitted operation identity for retry checkpoints.
func TransferTaskID(ctx context.Context) string {
	g, ok := ctx.Value(types.KBTransferContextKey).(kbTransferGrant)
	if !ok || g.caller != types.CallerFromContext(ctx) {
		return ""
	}
	return g.taskID
}

func validateTransferPair(
	source, target *types.KnowledgeBase,
	tenant uint64,
	operation KBTransferOperation,
	create bool,
) error {
	if source == nil || target == nil || tenant == 0 || source.ID == "" || target.ID == "" ||
		source.TenantID != tenant ||
		target.TenantID != tenant {
		return ErrForbidden
	}
	if source.ID == target.ID {
		return fmt.Errorf("source and target knowledge bases must differ")
	}
	if operation != KBTransferClone && operation != KBTransferMove || create && operation != KBTransferClone {
		return ErrForbidden
	}
	return nil
}

func transferAPIScope(
	ctx context.Context,
	sourceID, targetID string,
	operation KBTransferOperation,
	create bool,
) error {
	ids := []string{sourceID}
	if !create {
		ids = append(ids, targetID)
	}
	if err := types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, ids...); err != nil {
		return err
	}
	capability := types.APIKeyCapabilityManageKnowledgeBases
	if operation == KBTransferMove {
		capability = types.APIKeyCapabilityIngest
	}
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && !scope.FullAccess &&
		!scope.HasCapability(capability) {
		return ErrForbidden
	}
	return nil
}

// ValidateKBTransferCompatibility is shared by synchronous admission and task
// execution, so invalid modes/configurations fail before resource mutations.
func ValidateKBTransferCompatibility(
	source, target *types.KnowledgeBase,
	operation KBTransferOperation,
	mode string,
	tenant *types.Tenant,
) error {
	if source == nil || target == nil {
		return ErrNotFound
	}
	if source.ID == target.ID {
		return fmt.Errorf("source and target knowledge bases must differ")
	}
	if source.Type != target.Type {
		return fmt.Errorf("source and target knowledge bases must have the same type")
	}
	if source.EmbeddingModelID != target.EmbeddingModelID {
		return fmt.Errorf("source and target knowledge bases use different embedding models")
	}
	if operation == KBTransferMove && mode != "reuse_vectors" && mode != "reparse" {
		return fmt.Errorf("unknown move mode: %s", mode)
	}
	if (operation == KBTransferClone || mode == "reuse_vectors") && !source.SharesStoreWith(target) {
		return fmt.Errorf("source and target knowledge bases use different vector stores; use reparse mode for moves")
	}
	// A move retains file paths too, so resolving them through another storage
	// instance would make the document unreadable, even when vectors are reparsed.
	defaultID, defaultProvider := "", ""
	if tenant != nil {
		if tenant.DefaultStorageBackendID != nil {
			defaultID = *tenant.DefaultStorageBackendID
		}
		if tenant.StorageEngineConfig != nil {
			defaultProvider = tenant.StorageEngineConfig.DefaultProvider
		}
	}
	if !source.SharesStorageBackendWith(target, defaultID, defaultProvider) {
		return fmt.Errorf("source and target knowledge bases use different storage instances")
	}
	return nil
}
