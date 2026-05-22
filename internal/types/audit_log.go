package types

import (
	"time"
)

// AuditAction names a single audited action class. Action constants are
// dot-namespaced (`<area>.<event>`) so future PRs can plug in their own
// areas (e.g. `kb.shared`, `agent.copied`) without colliding with the
// RBAC events PR 6 ships.
type AuditAction string

const (
	// AuditActionMemberAdded fires when a tenant Owner / Admin adds a
	// new tenant_members row. The actor is the inviter; the target is
	// the invited user.
	AuditActionMemberAdded AuditAction = "rbac.member_added"
	// AuditActionMemberRemoved fires when an Owner / Admin removes a
	// tenant_members row. Distinct from MemberLeft so an audit reader
	// can tell "kicked out" from "left voluntarily".
	AuditActionMemberRemoved AuditAction = "rbac.member_removed"
	// AuditActionMemberRoleChanged fires for promote/demote operations.
	// The Details payload carries old_role and new_role.
	AuditActionMemberRoleChanged AuditAction = "rbac.member_role_changed"
	// AuditActionMemberLeft fires on POST /tenants/:id/leave — the
	// actor and target are the same user.
	AuditActionMemberLeft AuditAction = "rbac.member_left"
	// AuditActionAccessDenied fires when middleware/rbac.go's
	// RequireRole or RequireOwnershipOrRole rejects a request under
	// EnableRBAC=true. Subject to 1-minute sliding-window dedup so a
	// probing client cannot fill the table.
	AuditActionAccessDenied AuditAction = "rbac.access_denied"
	// AuditActionInvitationSent fires when an Owner issues a new
	// tenant invitation. Actor is the inviter, TargetUserID is the
	// invitee. Note: AuditActionMemberAdded only fires when the
	// invitee actually accepts and the tenant_members row is created.
	AuditActionInvitationSent AuditAction = "rbac.invitation_sent"
	// AuditActionInvitationAccepted fires when the invitee accepts a
	// pending invitation. Actor is the invitee (acting on their own
	// inbox); the matching rbac.member_added is emitted in the same
	// transaction.
	AuditActionInvitationAccepted AuditAction = "rbac.invitation_accepted"
	// AuditActionInvitationDeclined fires when the invitee rejects a
	// pending invitation. Actor and target are the same user.
	AuditActionInvitationDeclined AuditAction = "rbac.invitation_declined"
	// AuditActionInvitationRevoked fires when a tenant Owner cancels
	// a still-pending invitation before the invitee acts. Actor is
	// the Owner; target is the invitee.
	AuditActionInvitationRevoked AuditAction = "rbac.invitation_revoked"
	// AuditActionInvitationExpired fires when the lazy sweep transitions
	// an overdue pending row to expired. Actor is empty (system).
	AuditActionInvitationExpired AuditAction = "rbac.invitation_expired"

	// VectorStore lifecycle actions. Emitted by VectorStoreService.
	// Cover both env-store-derived (__env_*) and DB store create /
	// update / delete paths. Details payload identifies the store_id
	// and the changed key set; secret values (Password, APIKey,
	// connection_config encrypted blob) MUST NOT appear in details.

	// AuditActionVectorStoreCreated fires when a new VectorStore row
	// is committed to the DB (Phase 1 CRUD path). Actor is the tenant
	// user; resource is the VectorStore.
	AuditActionVectorStoreCreated AuditAction = "vector_store.created"
	// AuditActionVectorStoreUpdated fires on UPDATE of any VectorStore
	// mutable field. Details payload carries the changed key set
	// (never the secret values themselves — only the field names).
	AuditActionVectorStoreUpdated AuditAction = "vector_store.updated"
	// AuditActionVectorStoreDeleted fires when a VectorStore is
	// (soft-)deleted. Phase 2's delete guard already prevents deletion
	// of stores with bound KBs; the audit row records the actor and
	// the store_id for forensic traceability.
	AuditActionVectorStoreDeleted AuditAction = "vector_store.deleted"

	// OpenSearch-specific actions emitted by the driver shipped in
	// Phase 3. The OpenSearch index is a derived resource of the
	// VectorStore — these events capture cluster-side side effects
	// (PUT /<index>, DELETE /<index>, POST /_reindex) that operators
	// may need to correlate with VectorStore lifecycle events.

	// AuditActionOpenSearchIndexCreated fires when the OpenSearch
	// driver lazily creates a per-dimension index (the first time a
	// KB with a given embedding dim binds to the store). Details
	// payload: index name, alias name, dimension.
	AuditActionOpenSearchIndexCreated AuditAction = "opensearch.index_created"
	// AuditActionOpenSearchIndexDeleted fires when the OpenSearch
	// driver drops an index (e.g. cascade from VectorStore delete).
	AuditActionOpenSearchIndexDeleted AuditAction = "opensearch.index_deleted"
	// AuditActionOpenSearchReindexExecuted fires when CopyIndices
	// initiates a _reindex (sync or async). Details payload: source
	// KB id, target KB id, sync-or-async, doc count if known.
	AuditActionOpenSearchReindexExecuted AuditAction = "opensearch.reindex_executed"
)

// AuditOutcome distinguishes successful mutations from middleware-level
// rejections. The split lets the audit-log UI highlight denials in red
// without needing to enumerate every action class.
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeDenied  AuditOutcome = "denied"
)

// AuditLog is a single immutable audit event. The schema is intentionally
// generic: PR 6 wires only RBAC events, but TargetType / TargetID /
// Details are set up to absorb KB / agent / datasource events in
// follow-up PRs without another migration.
//
// Rows are append-only — no UpdatedAt, no soft-delete column. The
// monotonic ID acts as both primary key and pagination cursor (newest-
// first is `WHERE id < AfterID ORDER BY id DESC`).
type AuditLog struct {
	ID            uint64       `json:"id"             gorm:"primaryKey;autoIncrement"`
	TenantID      uint64       `json:"tenant_id"      gorm:"not null;index:idx_audit_logs_tenant_id_desc,priority:1;index:idx_audit_logs_tenant_action,priority:1"`
	ActorUserID   string       `json:"actor_user_id"  gorm:"type:varchar(36);default:'';index:idx_audit_logs_actor"`
	ActorRole     string       `json:"actor_role"     gorm:"type:varchar(32);default:''"`
	Action        AuditAction  `json:"action"         gorm:"type:varchar(64);not null;index:idx_audit_logs_tenant_action,priority:2"`
	TargetType    string       `json:"target_type"    gorm:"type:varchar(32);default:''"`
	TargetID      string       `json:"target_id"      gorm:"type:varchar(64);default:''"`
	TargetUserID  string       `json:"target_user_id" gorm:"type:varchar(36);default:''"`
	RequestPath   string       `json:"request_path"   gorm:"type:varchar(512);default:''"`
	RequestMethod string       `json:"request_method" gorm:"type:varchar(16);default:''"`
	Outcome       AuditOutcome `json:"outcome"        gorm:"type:varchar(16);default:success"`
	Details       JSON         `json:"details"        gorm:"type:jsonb;default:'{}'"`
	CreatedAt     time.Time    `json:"created_at"     gorm:"index:idx_audit_logs_tenant_id_desc,priority:2,sort:desc"`
}

// TableName pins the table name even if a future GORM convention
// pluralisation refactor would otherwise rename it.
func (AuditLog) TableName() string { return "audit_logs" }
