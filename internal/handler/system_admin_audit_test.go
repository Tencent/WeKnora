package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// capturingAuditService implements just AuditLogService.Log for the
// emitAdminAudit unit tests. Embedding the interface keeps any other
// method call a nil-deref panic, which surfaces a silent contract
// drift loudly rather than silently working.
type capturingAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
	logErr  error
}

func (c *capturingAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	c.entries = append(c.entries, entry)
	return c.logErr
}

// newSystemHandlerWithAudit constructs a SystemHandler with only the
// audit dependency wired. emitAdminAudit doesn't touch any other field,
// so leaving the rest nil intentionally crashes any future regression
// that drags an unrelated dep into the audit path.
func newSystemHandlerWithAudit(svc interfaces.AuditLogService) *SystemHandler {
	return &SystemHandler{auditSvc: svc}
}

// withActor builds a ctx carrying a user id under the canonical key, so
// the helper resolves a non-empty actor exactly the way the gin chain
// would in production.
func withActor(userID string) context.Context {
	return context.WithValue(context.Background(), types.UserIDContextKey, userID)
}

// TestEmitAdminAudit_NilServiceIsNoop pins the documented degraded
// mode: a handler constructed without an audit service must not panic
// when promote / revoke calls emitAdminAudit. This is what lets unit
// tests of the broader handler stay lightweight, and what protects
// the business path from a misconfigured container at runtime.
func TestEmitAdminAudit_NilServiceIsNoop(t *testing.T) {
	h := newSystemHandlerWithAudit(nil)
	// If this panicked the deferred t.Fatalf wouldn't fire — gating on
	// a nil receiver is the entire point of this case.
	h.emitAdminAudit(
		context.Background(),
		types.AuditActionSystemAdminPromoted,
		&types.User{ID: "u-target"},
		map[string]any{"target_email": "a@b.c"},
	)
}

// TestEmitAdminAudit_PopulatesCanonicalFields exercises the happy path
// and verifies every field the handler is responsible for: tenant_id=0
// (system scope), actor from context, action passed through, target
// fields derived from the user, outcome=success, role hard-pinned to
// "system_admin", and details serialised exactly once.
func TestEmitAdminAudit_PopulatesCanonicalFields(t *testing.T) {
	svc := &capturingAuditService{}
	h := newSystemHandlerWithAudit(svc)

	target := &types.User{ID: "u-target", Username: "wizardchen2", Email: "x@y.z"}
	details := map[string]any{
		"target_email":    target.Email,
		"target_username": target.Username,
		"idempotent":      false,
	}
	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminPromoted,
		target,
		details,
	)

	if len(svc.entries) != 1 {
		t.Fatalf("expected exactly 1 audit row, got %d", len(svc.entries))
	}
	got := svc.entries[0]
	if got.TenantID != 0 {
		t.Fatalf("system audit must use tenant_id=0, got %d", got.TenantID)
	}
	if got.ActorUserID != "u-actor" {
		t.Fatalf("actor must come from ctx, got %q", got.ActorUserID)
	}
	if got.ActorRole != "system_admin" {
		t.Fatalf("actor_role must be hard-pinned to 'system_admin', got %q", got.ActorRole)
	}
	if got.Action != types.AuditActionSystemAdminPromoted {
		t.Fatalf("expected action=system.admin_promoted, got %q", got.Action)
	}
	if got.Outcome != types.AuditOutcomeSuccess {
		t.Fatalf("expected outcome=success, got %q", got.Outcome)
	}
	if got.TargetType != "user" {
		t.Fatalf("expected target_type=user, got %q", got.TargetType)
	}
	// Both fields point at the same UUID so downstream filters can
	// match either one (rbac.* convention uses target_user_id;
	// generic readers use target_id).
	if got.TargetID != target.ID || got.TargetUserID != target.ID {
		t.Fatalf(
			"target ids must echo user.ID; got target_id=%q target_user_id=%q",
			got.TargetID, got.TargetUserID,
		)
	}

	// Details must be a JSON object whose keys round-trip. We don't
	// pin the byte-exact form because Go map iteration order is
	// non-deterministic.
	var roundTrip map[string]any
	if err := json.Unmarshal([]byte(got.Details), &roundTrip); err != nil {
		t.Fatalf("details must be valid JSON, got %q (err=%v)", string(got.Details), err)
	}
	if roundTrip["target_email"] != "x@y.z" {
		t.Fatalf("details.target_email lost in marshal: %v", roundTrip["target_email"])
	}
	if roundTrip["target_username"] != "wizardchen2" {
		t.Fatalf("details.target_username lost in marshal: %v", roundTrip["target_username"])
	}
	if roundTrip["idempotent"] != false {
		t.Fatalf("details.idempotent lost in marshal: %v", roundTrip["idempotent"])
	}
}

// TestEmitAdminAudit_NilDetailsLeavesEmptyPayload ensures the helper
// doesn't fabricate a payload when the caller passes nil — important
// because the audit_logs.details column defaults to '{}' at the DB
// layer, and emitting an explicit `null` would muddle filters that
// look for "no extra context".
func TestEmitAdminAudit_NilDetailsLeavesEmptyPayload(t *testing.T) {
	svc := &capturingAuditService{}
	h := newSystemHandlerWithAudit(svc)

	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminRevoked,
		&types.User{ID: "u-target"},
		nil,
	)
	if len(svc.entries) != 1 {
		t.Fatalf("expected exactly 1 audit row, got %d", len(svc.entries))
	}
	if len(svc.entries[0].Details) != 0 {
		t.Fatalf(
			"nil details must leave Details empty (DB default applies); got %q",
			string(svc.entries[0].Details),
		)
	}
}

// TestEmitAdminAudit_NilTargetStillEmitsRow defends the helper's
// nil-target branch. promote/revoke handlers always supply a target
// today, but the guard exists so future call sites (e.g. a "self
// service" revoke fired without a hydrated user) don't crash. The row
// still goes out with empty target ids — better than dropping it.
func TestEmitAdminAudit_NilTargetStillEmitsRow(t *testing.T) {
	svc := &capturingAuditService{}
	h := newSystemHandlerWithAudit(svc)

	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminPromoted,
		nil,
		map[string]any{"target_email": "lost@nowhere"},
	)
	if len(svc.entries) != 1 {
		t.Fatalf("expected exactly 1 audit row, got %d", len(svc.entries))
	}
	got := svc.entries[0]
	if got.TargetID != "" || got.TargetUserID != "" {
		t.Fatalf(
			"nil target must leave both target ids empty; got target_id=%q target_user_id=%q",
			got.TargetID, got.TargetUserID,
		)
	}
	// ActorRole / Action / TenantID still pin canonical values even
	// when the target is missing.
	if got.TenantID != 0 || got.ActorRole != "system_admin" {
		t.Fatalf("system-scope invariants must still hold, got %+v", got)
	}
}

// TestEmitAdminAudit_IdempotentBranchSurvivesMarshal covers the two
// boolean flags the promote / revoke flows use to discriminate real
// mutations from no-ops:
//
//   - promote idempotent=true: target was already a system admin; no
//     row was written, but we still emit an audit so probing the
//     endpoint leaves a trail.
//   - revoke changed=false: target was not a system admin to begin
//     with; the operation succeeded as a no-op, and the audit reader
//     should be able to distinguish this from a real revoke.
//
// Both flags are booleans, which JSON encodes faithfully — this case
// is the regression guard against someone accidentally swapping the
// payload to a stringly-typed shape ("true" / "false").
func TestEmitAdminAudit_IdempotentBranchSurvivesMarshal(t *testing.T) {
	svc := &capturingAuditService{}
	h := newSystemHandlerWithAudit(svc)

	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminPromoted,
		&types.User{ID: "u-target"},
		map[string]any{"idempotent": true},
	)
	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminRevoked,
		&types.User{ID: "u-target"},
		map[string]any{"changed": false},
	)
	if len(svc.entries) != 2 {
		t.Fatalf("expected exactly 2 audit rows, got %d", len(svc.entries))
	}

	var promote map[string]any
	if err := json.Unmarshal([]byte(svc.entries[0].Details), &promote); err != nil {
		t.Fatalf("unmarshal promote details: %v", err)
	}
	if promote["idempotent"] != true {
		t.Fatalf("promote.idempotent must round-trip as JSON bool, got %T(%v)", promote["idempotent"], promote["idempotent"])
	}

	var revoke map[string]any
	if err := json.Unmarshal([]byte(svc.entries[1].Details), &revoke); err != nil {
		t.Fatalf("unmarshal revoke details: %v", err)
	}
	if revoke["changed"] != false {
		t.Fatalf("revoke.changed must round-trip as JSON bool, got %T(%v)", revoke["changed"], revoke["changed"])
	}
}

// TestEmitAdminAudit_LogErrorIsSwallowed pins the best-effort contract
// documented at the top of emitAdminAudit. A failing audit write must
// NOT bubble up — the underlying business operation (promote / revoke)
// has already succeeded, and propagating the error would force the
// caller to either retry the privilege change or roll it back, both
// strictly worse than logging-and-continuing.
func TestEmitAdminAudit_LogErrorIsSwallowed(t *testing.T) {
	svc := &capturingAuditService{logErr: errors.New("transient db hiccup")}
	h := newSystemHandlerWithAudit(svc)

	// If this panicked or propagated the error, emitAdminAudit's
	// no-throw contract would be broken. The helper's signature is
	// `func(...)` with no return, so the test asserts behaviour by
	// surviving the call.
	h.emitAdminAudit(
		withActor("u-actor"),
		types.AuditActionSystemAdminPromoted,
		&types.User{ID: "u-target"},
		nil,
	)
	if len(svc.entries) != 1 {
		t.Fatalf("expected the audit attempt to be observed even on Log error, got %d entries", len(svc.entries))
	}
}
