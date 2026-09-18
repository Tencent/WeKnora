package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrAgentKBScopeNotShareable is returned when an agent share would expose
// knowledge bases its sharer could not share directly.
var ErrAgentKBScopeNotShareable = errors.New(
	"the agent's knowledge bases include ones you cannot share; only their creators or workspace admins can",
)

// kbBatchLookup loads knowledge bases by ID without a tenant filter.
type kbBatchLookup func(ctx context.Context, ids []string) ([]*types.KnowledgeBase, error)

// checkAgentKBScopeShareable applies the KB share rule (the KB's creator or a
// workspace Admin+, as on POST /knowledge-bases/:id/shares) to the KBs an
// agent share exposes: every member of the organization gets read access to
// the agent's KB scope, so sharing the agent must not reach KBs the sharer
// could not share on their own. "all" also covers KBs created later, so only
// an Admin may share it.
//
// Only KBs that after adds to before are checked, so editing a shared agent
// without widening its scope never fails; before is nil when the agent is
// being shared.
func checkAgentKBScopeShareable(
	ctx context.Context, lookup kbBatchLookup, before, after *types.CustomAgent, userID string,
) error {
	if canShareAnyKB(ctx) {
		return nil
	}
	previous := types.NewSharedAgentKBScope(before)
	next := types.NewSharedAgentKBScope(after)
	if next.IsAll() {
		if previous.IsAll() {
			return nil
		}
		return ErrAgentKBScopeNotShareable
	}
	var added []string
	for _, id := range next.IDs() {
		if !previous.Allows(id, after.TenantID) {
			added = append(added, id)
		}
	}
	if len(added) == 0 {
		return nil
	}
	kbs, err := lookup(ctx, added)
	if err != nil {
		return err
	}
	for _, kb := range kbs {
		// A KB of another workspace is outside an agent share's scope.
		if kb == nil || kb.TenantID != after.TenantID {
			continue
		}
		if kb.CreatorID == "" || kb.CreatorID != userID {
			return ErrAgentKBScopeNotShareable
		}
	}
	return nil
}

// canShareAnyKB mirrors the Admin branch of the KB share route guard; share
// management is limited to full-access API keys.
func canShareAnyKB(ctx context.Context) bool {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return scope.FullAccess
	}
	return types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin)
}
