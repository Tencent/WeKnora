package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestSandboxConnectionCheckConfigAllowsTemplateDiscoveryAfterConnection(t *testing.T) {
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "key"},
	}

	got := sandboxConnectionCheckConfig(incoming)

	require.Equal(t, "__connection_check__", got.E2B.TemplateID)
	require.Empty(t, incoming.E2B.TemplateID, "the submitted form must not be mutated")
}

func TestSandboxCheckReasonDockerUnavailableIncludesHost(t *testing.T) {
	msg := sandboxCheckReason(&sandbox.RemoteError{
		Kind:     sandbox.RemoteErrorKindUnavailable,
		Provider: sandbox.SandboxTypeDocker,
		Message:  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
	})
	require.Contains(t, msg, "unix:///var/run/docker.sock")
	require.Contains(t, msg, "docker context")
}

func TestRunStatelessSandboxCheckLocal(t *testing.T) {
	cfg := sandbox.DefaultConfig()
	cfg.Type = sandbox.SandboxTypeLocal
	result := &SandboxCheckResponse{OK: true, Provider: string(cfg.Type)}

	(&SystemHandler{}).runStatelessSandboxCheck(context.Background(), cfg, true, result)

	require.True(t, result.OK)
	require.Len(t, result.Checks, 2)
	require.Equal(t, "environment_available", result.Checks[0].Name)
	require.NotNil(t, result.Checks[0].OK)
	require.True(t, *result.Checks[0].OK)
	require.Equal(t, "sandbox_exec", result.Checks[1].Name)
	require.NotNil(t, result.Checks[1].OK)
	require.True(t, *result.Checks[1].OK)
}
