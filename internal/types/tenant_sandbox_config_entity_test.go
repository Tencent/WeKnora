package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTenantSandboxConfigEntityTableName(t *testing.T) {
	e := &TenantSandboxConfigEntity{}
	require.Equal(t, "tenant_sandbox_configs", e.TableName())
}

// A cordon is a lease, not a permanent lock: a handler that crashes while
// holding it must not wedge the config forever.
func TestTenantSandboxConfigEntityIsCordoned(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-30 * time.Second)
	stale := now.Add(-10 * time.Minute)

	tests := []struct {
		name string
		at   *time.Time
		want bool
	}{
		{name: "never cordoned", at: nil, want: false},
		{name: "fresh cordon blocks", at: &fresh, want: true},
		{name: "stale cordon is ignored", at: &stale, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &TenantSandboxConfigEntity{CordonedAt: tt.at}
			require.Equal(t, tt.want, e.IsCordoned(now, SandboxCordonLease))
		})
	}
}

// A config row that was not found comes back as nil; treating that as cordoned
// would refuse sandbox resolution for a config that does not exist.
func TestNilEntityIsNotCordoned(t *testing.T) {
	var e *TenantSandboxConfigEntity
	require.False(t, e.IsCordoned(time.Now(), SandboxCordonLease))
}

// The value is persisted in sessions.sandbox_config_id and stamped onto sandbox
// metadata, so changing it would orphan existing rows and sandboxes.
func TestSandboxConfigIDGlobalDefaultValue(t *testing.T) {
	require.Equal(t, "-", SandboxConfigIDGlobalDefault)
}
