package approval

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type batchPolicyService struct {
	stubChecker
	rows    []*types.MCPToolApproval
	listErr error
	reads   int
}

func (s *batchPolicyService) ListByService(context.Context, uint64, string) ([]*types.MCPToolApproval, error) {
	s.reads++
	return s.rows, s.listErr
}

func TestGateBatchPolicyScopesDefaultsAndFailures(t *testing.T) {
	svc := &batchPolicyService{rows: []*types.MCPToolApproval{
		{TenantID: 7, ServiceID: "svc", ToolName: "disabled", Enabled: false},
		{TenantID: 8, ServiceID: "svc", ToolName: "default", Enabled: false},
		{TenantID: 7, ServiceID: "other", ToolName: "default", Enabled: false},
		{TenantID: 7, ServiceID: "svc", ToolName: "not-requested", Enabled: true},
	}}
	gate := NewGate(nil, &Adapter{Svc: svc}, nil)
	ctx := context.Background()
	result, err := EnabledTools(ctx, gate, 7, "svc", []string{"default", "disabled"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"default": true, "disabled": false}, result)
	require.Equal(t, 1, svc.reads)
	_, err = gate.EnabledTools(ctx, 0, "svc", []string{"default"})
	require.Error(t, err)
	require.Equal(t, 1, svc.reads)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = gate.EnabledTools(canceled, 7, "svc", []string{"default"})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, svc.reads)
	svc.listErr = errors.New("database unavailable")
	result, err = gate.EnabledTools(ctx, 7, "svc", []string{"default"})
	require.ErrorIs(t, err, svc.listErr)
	require.Nil(t, result)
}

func TestGateBatchPolicyLegacyCheckerAndNoChecker(t *testing.T) {
	disabled := false
	gate := NewGate(nil, &Adapter{Svc: &stubChecker{enabled: &disabled}}, nil)
	result, err := gate.EnabledTools(context.Background(), 7, "svc", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"a": false, "b": false}, result)
	result, err = NewGate(nil, nil, nil).EnabledTools(context.Background(), 7, "svc", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"a": true, "b": true}, result)
}
