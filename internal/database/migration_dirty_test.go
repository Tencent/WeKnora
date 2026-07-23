package database

import (
	"strings"
	"testing"
)

// C3: MySQL dirty migration must fail-closed.
//
// MySQL DDL is not transactional, so auto-recovering a dirty migration by
// forcing the version backward and re-running Up() can leave a half-applied
// schema that silently corrupts subsequent business queries. PG/SQLite wrap
// each migration step in a transaction so the existing auto-recover path is
// safe there; MySQL must instead surface an actionable error and let the
// operator inspect the schema manually.
//
// The error message must:
//  1. Name the dirty version.
//  2. Explain *why* auto-recovery is disabled (MySQL DDL is not transactional).
//  3. Tell the operator the two recovery options: force the version, or drop
//     and recreate the database.
//
// dirtyStateErrorMessage is a pure helper extracted so the message shape is
// unit-testable without standing up a live migrate instance.

func TestDirtyStateErrorMessage_MySQLMentionsDDLAndManualOptions(t *testing.T) {
	got := dirtyStateErrorMessage(7, true)
	if !strings.Contains(got, "7") {
		t.Errorf("error must name the dirty version; got: %s", got)
	}
	if !strings.Contains(got, "MySQL DDL is not transactional") {
		t.Errorf("error must explain why auto-recovery is disabled; got: %s", got)
	}
	if !strings.Contains(got, "force the") {
		t.Errorf("error must mention the force recovery option; got: %s", got)
	}
	if !strings.Contains(got, "drop and recreate") {
		t.Errorf("error must mention the drop-and-recreate option; got: %s", got)
	}
}

// FailOnDirty overrides AutoRecoverDirty: when FailOnDirty is true, the
// migrator MUST NOT call recoverFromDirtyState, regardless of the
// AutoRecoverDirty value. This keeps MySQL fail-closed even if an operator
// leaves AUTO_RECOVER_DIRTY=true.
func TestMigrationOptions_FailOnDirtyTakesPrecedence(t *testing.T) {
	opts := MigrationOptions{AutoRecoverDirty: true, FailOnDirty: true}
	if !opts.FailOnDirty {
		t.Error("FailOnDirty=true must win over AutoRecoverDirty=true")
	}
	opts = MigrationOptions{AutoRecoverDirty: true, FailOnDirty: false}
	if opts.FailOnDirty {
		t.Error("FailOnDirty=false must let AutoRecoverDirty drive recovery")
	}
}
