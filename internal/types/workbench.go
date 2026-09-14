package types

// WorkbenchAuthorization contains only the live identity fields needed by the
// workbench. It deliberately excludes credentials, configuration and role bypasses.
type WorkbenchAuthorization struct {
	UserID       string
	UserActive   bool
	TenantID     uint64
	TenantStatus string
	MemberStatus TenantMemberStatus
}
