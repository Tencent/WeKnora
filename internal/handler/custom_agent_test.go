package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateAgentSandboxConfigRejectsAnyConfigOnLite(t *testing.T) {
	h := &CustomAgentHandler{desktop: true}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	err := h.validateAgentSandboxConfig(ctx, types.CustomAgentConfig{SandboxConfigID: "cfg-1"})
	require.Error(t, err)
	require.NoError(t, h.validateAgentSandboxConfig(ctx, types.CustomAgentConfig{}))
}
