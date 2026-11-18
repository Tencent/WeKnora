package allocation

import (
	"errors"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/sandbox/workspace"
)

// OwnerKind classifies who sessions.user_id names.
type OwnerKind string

// Kinds derived from owner_auth_source and the shape of user_id.
const (
	KindUnknown         OwnerKind = ""
	KindWebUser         OwnerKind = "web_user"
	KindAPIExternalUser OwnerKind = "api_external_user"
	KindAPITenantKey    OwnerKind = "api_tenant_key"
	KindAPIPlatformKey  OwnerKind = "api_platform_key"
	KindMCPEndpoint     OwnerKind = "mcp_endpoint"
	KindEmbed           OwnerKind = "embed"
	KindIM              OwnerKind = "im"
)

// AuthSource is how the server authenticated the owner when the session was
// created or its owner was first bound. It is recorded server-side only.
type AuthSource string

// Sources stamped by the server. SourceUnknown is an empty or legacy row.
const (
	SourceUnknown         AuthSource = "unknown"
	SourceWeb             AuthSource = "web"
	SourceAPIDirectHeader AuthSource = "api_direct_header"
	SourceAPISignedToken  AuthSource = "api_signed_token"
	SourceAPITenantKey    AuthSource = "api_tenant_key"
	SourceAPIPlatformKey  AuthSource = "api_platform_key"
	SourceMCPEndpoint     AuthSource = "mcp_endpoint"
	SourceEmbed           AuthSource = "embed"
	SourceIM              AuthSource = "im"
	SourceInternal        AuthSource = "internal"
)

// MaxOwnerIDLength matches sessions.user_id.
const MaxOwnerIDLength = 512

// Owner is the persisted owner of a session as the allocator sees it.
type Owner struct {
	TenantID uint64
	ID       string
	Kind     OwnerKind
	Source   AuthSource
}

// ValidateOwnerID rejects ids that cannot be part of a machine identity.
func ValidateOwnerID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("allocation: owner id is empty")
	}
	if len(id) > MaxOwnerIDLength {
		return errors.New("allocation: owner id is too long")
	}
	if strings.ContainsAny(id, "{}") {
		return errors.New("allocation: owner id must not contain braces")
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return errors.New("allocation: owner id must not contain control characters")
		}
	}
	return nil
}

// DecideInput is everything Decide needs; callers load it, Decide is pure.
type DecideInput struct {
	ConfigMode        Mode
	Purpose           Purpose
	Owner             Owner
	SessionID         string
	UserModeAvailable bool
}

// Decision is the mode a session gets and why it differs from the config.
type Decision struct {
	Mode   Mode
	Reason FallbackReason
}

// Decide applies the user-mode admission whitelist. Only a server-verified web
// account or API external user may share a machine; every other owner keeps a
// machine per session. Checks run in a fixed order so the recorded reason is
// deterministic: maintenance, deployment gate, invalid owner, shared entry or
// channel, unverified identity, session id shape.
func Decide(in DecideInput) Decision {
	session := func(reason FallbackReason) Decision { return Decision{Mode: ModeSession, Reason: reason} }
	if in.Purpose == PurposeMaintenance {
		return session(ReasonMaintenanceDedicated)
	}
	if in.ConfigMode != ModeUser {
		return session(ReasonNone)
	}
	if !in.UserModeAvailable {
		return session(ReasonUserModeUnavailable)
	}
	if strings.TrimSpace(in.Owner.ID) == "" {
		return session(ReasonOwnerIdentityUnverified)
	}
	if ValidateOwnerID(in.Owner.ID) != nil {
		return session(ReasonOwnerIdentityInvalid)
	}
	if EligibleForUser(in.Owner.Kind, in.Owner.Source) {
		if !workspace.IsCanonicalUUID(in.SessionID) {
			return session(ReasonSessionIDNotUUID)
		}
		return Decision{Mode: ModeUser}
	}
	switch in.Owner.Kind {
	case KindAPITenantKey, KindAPIPlatformKey:
		return session(ReasonSharedAPIKey)
	case KindMCPEndpoint:
		return session(ReasonSharedMCPEndpoint)
	case KindEmbed, KindIM:
		return session(ReasonChannelIdentity)
	default:
		return session(ReasonOwnerIdentityUnverified)
	}
}

// EligibleForUser is the whitelist itself: a server-verified web account, or
// an API external user whose subject came from the direct header or a signed
// token. Decide applies it at first use; the manager re-applies it to stored
// assignments so a row whose identity is no longer acceptable stops running.
func EligibleForUser(kind OwnerKind, source AuthSource) bool {
	switch kind {
	case KindWebUser:
		return source == SourceWeb
	case KindAPIExternalUser:
		return source == SourceAPIDirectHeader || source == SourceAPISignedToken
	default:
		return false
	}
}
