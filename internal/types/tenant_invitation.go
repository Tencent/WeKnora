package types

import (
	"time"

	"gorm.io/gorm"
)

// TenantInvitationStatus enumerates the lifecycle states of a single
// invitation row. The legal transitions are:
//
//	pending -> accepted | declined | revoked | expired
//
// All non-pending states are terminal; once a row leaves pending it is
// kept for the audit trail and a new pending row can be issued.
type TenantInvitationStatus string

const (
	// TenantInvitationStatusPending is the initial state: the invitee
	// has not yet acted and the row has not been revoked or aged out.
	TenantInvitationStatusPending TenantInvitationStatus = "pending"
	// TenantInvitationStatusAccepted means the invitee confirmed and
	// a corresponding active tenant_members row has been created in
	// the same transaction.
	TenantInvitationStatusAccepted TenantInvitationStatus = "accepted"
	// TenantInvitationStatusDeclined means the invitee rejected the
	// invitation. The row stays for auditability; a new pending
	// invitation can be issued afterwards.
	TenantInvitationStatusDeclined TenantInvitationStatus = "declined"
	// TenantInvitationStatusRevoked means a tenant Owner cancelled
	// the pending invitation before the invitee acted.
	TenantInvitationStatusRevoked TenantInvitationStatus = "revoked"
	// TenantInvitationStatusExpired means the row outlived its
	// expires_at without being accepted/declined/revoked. Set by the
	// lazy-sweep run before every List/Accept/Decline.
	TenantInvitationStatusExpired TenantInvitationStatus = "expired"
)

// IsTerminal reports whether s is a non-pending state. Used by the
// service layer to short-circuit accept/decline/revoke on rows that
// have already been finalised.
func (s TenantInvitationStatus) IsTerminal() bool {
	switch s {
	case TenantInvitationStatusAccepted,
		TenantInvitationStatusDeclined,
		TenantInvitationStatusRevoked,
		TenantInvitationStatusExpired:
		return true
	}
	return false
}

// TenantInvitation is one pending or finalised invitation issued by an
// Owner of `TenantID` to the user identified by `InviteeUserID`. The
// row is created in `pending` state when the Owner clicks "invite"; it
// flips to a terminal state when the invitee accepts/declines, the
// Owner revokes, or the row expires.
//
// A tenant_members row is created only on accept; the invitation table
// is therefore the single source of truth for "pending intent" without
// polluting the authoritative member roster.
type TenantInvitation struct {
	// Surrogate primary key.
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// TenantID references tenants.id.
	TenantID uint64 `json:"tenant_id" gorm:"not null;index"`
	// InviteeUserID references users.id (always a registered user; the
	// handler resolves the email to a User row before insertion).
	InviteeUserID string `json:"invitee_user_id" gorm:"type:varchar(36);not null;index"`
	// InvitedBy records the user id that issued this invitation. NULL
	// for invitations created via service-internal / synthetic actors
	// (mirrors the same treatment TenantMember.InvitedBy gets).
	InvitedBy *string `json:"invited_by,omitempty" gorm:"type:varchar(36)"`
	// Role the invitee will receive in tenant_members if they accept.
	Role TenantRole `json:"role" gorm:"type:varchar(20);not null"`
	// Status holds the lifecycle state. Default pending; mutated to
	// accepted/declined/revoked/expired exactly once.
	Status TenantInvitationStatus `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	// Message is an optional free-text note the Owner can include in
	// the invitation (e.g. "joining the design squad — welcome!").
	Message string `json:"message,omitempty" gorm:"type:varchar(500)"`
	// ExpiresAt is when this row auto-flips to expired if still pending.
	// Set at creation time from RBAC_INVITATION_TTL (default 7d).
	ExpiresAt time.Time `json:"expires_at"`
	// RespondedAt records when the row left pending. Nil while pending.
	RespondedAt *time.Time     `json:"responded_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName binds TenantInvitation to the tenant_invitations table.
func (TenantInvitation) TableName() string { return "tenant_invitations" }

// IsExpired reports whether this invitation is past its expires_at at
// the given reference time. The service layer uses this both for the
// lazy sweep and to reject an accept/decline arriving after timeout.
func (inv *TenantInvitation) IsExpired(at time.Time) bool {
	if inv == nil || inv.ExpiresAt.IsZero() {
		return false
	}
	return inv.ExpiresAt.Before(at)
}

// TenantInvitationResponse is the API projection joined with tenant
// name and inviter / invitee user fields the UI needs. The model is
// intentionally NOT serialised directly so we don't leak DeletedAt /
// UpdatedAt and lock the DB schema into the public API.
type TenantInvitationResponse struct {
	ID            uint64                 `json:"id"`
	TenantID      uint64                 `json:"tenant_id"`
	TenantName    string                 `json:"tenant_name,omitempty"`
	InviteeUserID string                 `json:"invitee_user_id"`
	InviteeEmail  string                 `json:"invitee_email,omitempty"`
	InviteeName   string                 `json:"invitee_name,omitempty"`
	InvitedBy     *string                `json:"invited_by,omitempty"`
	InviterEmail  string                 `json:"inviter_email,omitempty"`
	InviterName   string                 `json:"inviter_name,omitempty"`
	Role          TenantRole             `json:"role"`
	Status        TenantInvitationStatus `json:"status"`
	Message       string                 `json:"message,omitempty"`
	ExpiresAt     time.Time              `json:"expires_at"`
	RespondedAt   *time.Time             `json:"responded_at,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
}
