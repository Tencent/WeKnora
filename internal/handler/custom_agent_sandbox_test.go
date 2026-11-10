package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubSandboxConfigLookup answers the existence check without a database.
type stubSandboxConfigLookup struct {
	entity *types.TenantSandboxConfigEntity
	err    error

	lookups []string
}

func (s *stubSandboxConfigLookup) Get(
	_ context.Context, _ uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	s.lookups = append(s.lookups, id)
	return s.entity, s.err
}

func agentTenantContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
}

// The field must round-trip through the agent's JSON config column, since that
// is where it is persisted (no dedicated column).
func TestAgentConfigCarriesSandboxConfigID(t *testing.T) {
	cfg := types.CustomAgentConfig{
		AgentMode:       "smart-reasoning",
		SandboxConfigID: "cfg-a",
	}

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"sandbox_config_id":"cfg-a"`)

	var back types.CustomAgentConfig
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Equal(t, "cfg-a", back.SandboxConfigID)
}

// Empty must be omitted so existing agents keep an unchanged config payload.
func TestAgentConfigOmitsEmptySandboxConfigID(t *testing.T) {
	raw, err := json.Marshal(types.CustomAgentConfig{AgentMode: "quick-answer"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sandbox_config_id")
}

// Saving a dangling reference must fail here with a readable message. Deferring
// it means the agent looks fine until its first skill execution, which then dies
// on an opaque resolver error mid-conversation.
func TestAgentSandboxConfigValidationRejectsUnknownID(t *testing.T) {
	lookup := &stubSandboxConfigLookup{}
	h := &CustomAgentHandler{sandboxConfigs: lookup}

	err := h.validateAgentSandboxConfig(agentTenantContext(),
		types.CustomAgentConfig{SandboxConfigID: "cfg-gone"})

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Equal(t, []string{"cfg-gone"}, lookup.lookups)
}

func TestAgentSandboxConfigValidationAcceptsExistingID(t *testing.T) {
	lookup := &stubSandboxConfigLookup{
		entity: &types.TenantSandboxConfigEntity{ID: "cfg-a", TenantID: 7},
	}
	h := &CustomAgentHandler{sandboxConfigs: lookup}

	require.NoError(t, h.validateAgentSandboxConfig(agentTenantContext(),
		types.CustomAgentConfig{SandboxConfigID: "cfg-a"}))
}

// Most agents run on the deployment default, so the common path must not pay for
// a lookup - and must never be rejected for leaving the field blank.
func TestAgentSandboxConfigValidationSkipsEmptyID(t *testing.T) {
	lookup := &stubSandboxConfigLookup{}
	h := &CustomAgentHandler{sandboxConfigs: lookup}

	require.NoError(t, h.validateAgentSandboxConfig(agentTenantContext(),
		types.CustomAgentConfig{SandboxConfigID: "   "}))
	require.Empty(t, lookup.lookups)
}

// A lookup failure is not the admin's fault; it must not read as "you picked a
// config that does not exist".
func TestAgentSandboxConfigValidationSurfacesLookupFailure(t *testing.T) {
	lookup := &stubSandboxConfigLookup{err: stderrors.New("database is on fire")}
	h := &CustomAgentHandler{sandboxConfigs: lookup}

	err := h.validateAgentSandboxConfig(agentTenantContext(),
		types.CustomAgentConfig{SandboxConfigID: "cfg-a"})

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.HTTPCode)
}

// Partially-wired handlers appear in tests and in deployments without the
// config service; they must not reject every agent that selects a backend.
func TestAgentSandboxConfigValidationSkipsWhenServiceMissing(t *testing.T) {
	h := &CustomAgentHandler{}

	require.NoError(t, h.validateAgentSandboxConfig(agentTenantContext(),
		types.CustomAgentConfig{SandboxConfigID: "cfg-a"}))
}
