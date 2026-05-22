package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAuditAction_DotNamespaceConvention ensures every AuditAction
// constant follows the dot-namespaced `<area>.<event>` convention
// documented at the top of audit_log.go. Future PRs that add actions
// must keep this invariant so audit-log consumers can prefix-filter
// by area.
func TestAuditAction_DotNamespaceConvention(t *testing.T) {
	all := []AuditAction{
		// RBAC namespace (Phase 2 PR 5 / #1427)
		AuditActionMemberAdded,
		AuditActionMemberRemoved,
		AuditActionMemberRoleChanged,
		AuditActionMemberLeft,
		AuditActionAccessDenied,
		AuditActionInvitationSent,
		AuditActionInvitationAccepted,
		AuditActionInvitationDeclined,
		AuditActionInvitationRevoked,
		AuditActionInvitationExpired,
		// VectorStore namespace (Phase 3 PR 1 / #1440)
		AuditActionVectorStoreCreated,
		AuditActionVectorStoreUpdated,
		AuditActionVectorStoreDeleted,
		// OpenSearch namespace (Phase 3 PR 1 / #1440)
		AuditActionOpenSearchIndexCreated,
		AuditActionOpenSearchIndexDeleted,
		AuditActionOpenSearchReindexExecuted,
	}
	for _, a := range all {
		s := string(a)
		area, event, ok := strings.Cut(s, ".")
		assert.True(t, ok,
			"action %q must contain exactly one dot separator", s)
		assert.NotEmpty(t, area, "action %q has empty area", s)
		assert.NotEmpty(t, event, "action %q has empty event", s)
	}
}

// TestAuditAction_VectorStoreNamespacePrefix pins the three
// vector_store.* actions added in Phase 3 PR 1 to their shared area
// prefix. The prefix is the contract by which an audit-log reader
// can subscribe to "all VectorStore lifecycle events" with a single
// filter.
func TestAuditAction_VectorStoreNamespacePrefix(t *testing.T) {
	cases := []AuditAction{
		AuditActionVectorStoreCreated,
		AuditActionVectorStoreUpdated,
		AuditActionVectorStoreDeleted,
	}
	for _, a := range cases {
		assert.True(t,
			strings.HasPrefix(string(a), "vector_store."),
			"expected %q to start with 'vector_store.'", a,
		)
	}
}

// TestAuditAction_OpenSearchNamespacePrefix pins the three
// opensearch.* actions added in Phase 3 PR 1 to their shared area
// prefix. OpenSearch index lifecycle is a driver-specific concern
// distinct from VectorStore lifecycle — both can co-occur for the
// same logical operation (e.g. CreateVectorStore + IndexCreated).
func TestAuditAction_OpenSearchNamespacePrefix(t *testing.T) {
	cases := []AuditAction{
		AuditActionOpenSearchIndexCreated,
		AuditActionOpenSearchIndexDeleted,
		AuditActionOpenSearchReindexExecuted,
	}
	for _, a := range cases {
		assert.True(t,
			strings.HasPrefix(string(a), "opensearch."),
			"expected %q to start with 'opensearch.'", a,
		)
	}
}

// TestAuditAction_NoCollisionsAcrossNamespaces ensures no two
// AuditAction constants share the same wire string. A duplicate would
// silently merge two logical events into one entry in the audit-log
// UI, defeating the dot-namespace convention.
func TestAuditAction_NoCollisionsAcrossNamespaces(t *testing.T) {
	seen := make(map[AuditAction]string)
	register := func(name string, a AuditAction) {
		t.Helper()
		if prev, exists := seen[a]; exists {
			t.Fatalf("collision: %s and %s both map to %q", prev, name, a)
		}
		seen[a] = name
	}
	register("AuditActionMemberAdded", AuditActionMemberAdded)
	register("AuditActionMemberRemoved", AuditActionMemberRemoved)
	register("AuditActionMemberRoleChanged", AuditActionMemberRoleChanged)
	register("AuditActionMemberLeft", AuditActionMemberLeft)
	register("AuditActionAccessDenied", AuditActionAccessDenied)
	register("AuditActionInvitationSent", AuditActionInvitationSent)
	register("AuditActionInvitationAccepted", AuditActionInvitationAccepted)
	register("AuditActionInvitationDeclined", AuditActionInvitationDeclined)
	register("AuditActionInvitationRevoked", AuditActionInvitationRevoked)
	register("AuditActionInvitationExpired", AuditActionInvitationExpired)
	register("AuditActionVectorStoreCreated", AuditActionVectorStoreCreated)
	register("AuditActionVectorStoreUpdated", AuditActionVectorStoreUpdated)
	register("AuditActionVectorStoreDeleted", AuditActionVectorStoreDeleted)
	register("AuditActionOpenSearchIndexCreated", AuditActionOpenSearchIndexCreated)
	register("AuditActionOpenSearchIndexDeleted", AuditActionOpenSearchIndexDeleted)
	register("AuditActionOpenSearchReindexExecuted", AuditActionOpenSearchReindexExecuted)
}

// TestAuditAction_Phase3WireValues pins the exact wire strings for
// the six new Phase 3 actions. The wire strings are the public
// contract for audit-log consumers; changing them is a breaking
// change.
func TestAuditAction_Phase3WireValues(t *testing.T) {
	cases := []struct {
		constant AuditAction
		wire     string
	}{
		{AuditActionVectorStoreCreated, "vector_store.created"},
		{AuditActionVectorStoreUpdated, "vector_store.updated"},
		{AuditActionVectorStoreDeleted, "vector_store.deleted"},
		{AuditActionOpenSearchIndexCreated, "opensearch.index_created"},
		{AuditActionOpenSearchIndexDeleted, "opensearch.index_deleted"},
		{AuditActionOpenSearchReindexExecuted, "opensearch.reindex_executed"},
	}
	for _, c := range cases {
		assert.Equal(t, c.wire, string(c.constant))
	}
}
