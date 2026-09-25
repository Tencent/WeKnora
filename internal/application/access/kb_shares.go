package access

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KBSharePermissions resolves organization grants for one caller during one
// operation. It deliberately does not grant tenant ownership or shared-agent
// access. Callers retain their own policy for lookup failures (abort or filter).
// Create a new instance for each operation; it is not a cross-request cache and
// must not be shared between goroutines.
type KBSharePermissions struct {
	ctx      context.Context
	lookup   KBShareLookup
	tenantID uint64
	role     types.TenantRole
	results  map[string]kbSharePermission
}

type kbSharePermission struct {
	role   types.OrgMemberRole
	shared bool
	err    error
}

// NewKBSharePermissions creates a request-local permission cache.
func NewKBSharePermissions(
	ctx context.Context,
	lookup KBShareLookup,
	tenantID uint64,
	role types.TenantRole,
) *KBSharePermissions {
	return &KBSharePermissions{
		ctx:      ctx,
		lookup:   lookup,
		tenantID: tenantID,
		role:     role,
		results:  make(map[string]kbSharePermission),
	}
}

// Permission caches both grants and failures so multiple documents/chunks in a
// KB observe the same decision, without repeating the membership queries.
func (p *KBSharePermissions) Permission(kbID string) (types.OrgMemberRole, bool, error) {
	if kbID == "" || p.tenantID == 0 || p.lookup == nil {
		return "", false, nil
	}
	result, ok := p.results[kbID]
	if !ok {
		result.role, result.shared, result.err = p.lookup.CheckTenantKBPermission(p.ctx, kbID, p.tenantID, p.role)
		p.results[kbID] = result
	}
	return result.role, result.shared, result.err
}

// Check tests the required role against the cached organization permission.
func (p *KBSharePermissions) Check(kbID string, required types.OrgMemberRole) (bool, error) {
	role, shared, err := p.Permission(kbID)
	return err == nil && shared && role.IsValid() && required.IsValid() && role.HasPermission(required), err
}
