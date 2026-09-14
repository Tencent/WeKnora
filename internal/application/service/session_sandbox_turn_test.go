package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHoldSandboxTurnOpensAndClosesTheLease(t *testing.T) {
	holder := &turnLeaseManager{}
	svc := &sessionService{sandboxMgr: holder}

	release := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.Equal(t, 1, holder.begins)
	require.Zero(t, holder.ends)

	release()
	require.Equal(t, 1, holder.ends)
}

func TestHoldSandboxTurnIsNoopWhenBeginFails(t *testing.T) {
	holder := &turnLeaseManager{beginErr: context.Canceled}
	svc := &sessionService{sandboxMgr: holder}

	release := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.Equal(t, 1, holder.begins)
	release()
	require.Zero(t, holder.ends)
}

type turnLeaseManager struct {
	stagingSandboxManager
	begins   int
	ends     int
	beginErr error
	endErr   error
}

func (m *turnLeaseManager) BeginSessionTurn(context.Context, string) error {
	m.begins++
	return m.beginErr
}

func (m *turnLeaseManager) EndSessionTurn(context.Context, string) error {
	m.ends++
	return m.endErr
}
