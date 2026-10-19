package approval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// GrantStore keeps the approvals a user allowed to be remembered.
type GrantStore interface {
	FindGrant(ctx context.Context, tenantID uint64, userID string, scope types.GrantScope, key string) (
		*types.AgentGrant, error)
	SaveGrant(ctx context.Context, grant *types.AgentGrant) error
	TouchGrant(ctx context.Context, id string, at time.Time) error
}

// WithGrants makes the gate remember "allow for the rest" approvals in
// store. Without one, every call asks.
func (g *Gate) WithGrants(store GrantStore) *Gate {
	g.grants = store
	return g
}

// grantScope is where grants of a run on ctx live: the scope the run set,
// else the session of the call.
func grantScope(ctx context.Context, sessionID string) (types.GrantScope, bool) {
	if scope, ok := types.GrantScopeFromContext(ctx); ok {
		return scope, true
	}
	if sessionID == "" {
		return types.GrantScope{}, false
	}
	return types.GrantScope{Scope: types.AgentGrantScopeSession, ScopeID: sessionID}, true
}

// granted reports whether the user already allowed calls with req's grant
// in the run's scope. When they had but it no longer applies, lapse says
// why; a revoked grant is not reported, since the user withdrew it. Any
// lookup failure asks again.
func (g *Gate) granted(ctx context.Context, req PendingRequest) (ok bool, lapse string) {
	if g.grants == nil || req.Grant == "" || req.UserID == "" || req.TenantID == 0 {
		return false, ""
	}
	scope, ok := grantScope(ctx, req.SessionID)
	if !ok {
		return false, ""
	}
	grant, err := g.grants.FindGrant(ctx, req.TenantID, req.UserID, scope, req.Grant)
	if err != nil {
		logger.Warnf(ctx, "tool approval: read grant: %v", err)
		return false, ""
	}
	if grant == nil {
		return false, ""
	}
	now := time.Now()
	if lapse := grant.LapseAt(now, scope.BindingHash); lapse != "" {
		if lapse == types.AgentGrantLapseRevoked {
			return false, ""
		}
		logger.Infof(ctx, "tool approval: grant %s %s; asking again", grant.ID, lapse)
		return false, lapse
	}
	if err := g.grants.TouchGrant(ctx, grant.ID, now); err != nil {
		logger.Warnf(ctx, "tool approval: record grant use: %v", err)
	}
	return true, ""
}

// remember stores the grant an approved prompt allowed for the rest of the
// run's scope.
func (g *Gate) remember(ctx context.Context, d Decision, prompt *types.AgentPrompt) {
	if g.grants == nil || prompt == nil || !d.Approved || !d.AllowInSession {
		return
	}
	var request types.ToolApprovalPromptRequest
	if json.Unmarshal(prompt.Request, &request) != nil || request.Grant == "" {
		return
	}
	scope, ok := grantScope(ctx, prompt.SessionID)
	if !ok || prompt.TenantID == 0 || prompt.UserID == "" {
		return
	}
	grant := &types.AgentGrant{
		TenantID: prompt.TenantID, UserID: prompt.UserID,
		Scope: scope.Scope, ScopeID: scope.ScopeID, GrantKey: request.Grant,
		Label: request.GrantScope, BindingHash: scope.BindingHash, PromptID: prompt.ID,
	}
	if err := g.grants.SaveGrant(context.WithoutCancel(ctx), grant); err != nil {
		logger.Warnf(ctx, "tool approval: remember grant: %v", err)
	}
}
