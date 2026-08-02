package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// rerankResolutionModelService is a minimal model-service stub exposing only
// ListModels. ResolveRerankModel and the auto-detect fallback both read the
// model list from here.
type rerankResolutionModelService struct {
	interfaces.ModelService
	models    []*types.Model
	listErr   error
	listCalls int
}

func (s *rerankResolutionModelService) ListModels(context.Context) ([]*types.Model, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.models, nil
}

func newRerankResolutionService(ms *rerankResolutionModelService) *sessionService {
	return &sessionService{modelService: ms}
}

// rerankModel returns a ready-made rerank model; status overrides to inactive.
func rerankModel(id, name string, active bool) *types.Model {
	status := types.ModelStatusActive
	if !active {
		status = types.ModelStatusDownloading
	}
	return &types.Model{ID: id, Name: name, Type: types.ModelTypeRerank, Status: status}
}

func activeRerankModels() []*types.Model {
	return []*types.Model{
		rerankModel("rerank-1", "rerank-a", true),
		rerankModel("rerank-2", "rerank-b", true),
	}
}

// --- ResolveRerankModel (low-level helper) ---

// An empty requested string means "no override": returns empty with no
// error, leaving resolution to the chain (tenant config -> auto-detect).
func TestResolveRerankModel_EmptyRequest_ReturnsEmpty(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.ResolveRerankModel(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// UUID exact match wins, regardless of name collisions.
func TestResolveRerankModel_IDMatch(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.ResolveRerankModel(context.Background(), "rerank-1")
	require.NoError(t, err)
	assert.Equal(t, "rerank-1", got)
}

// When no ID matches, a name matching exactly one active rerank model
// resolves to that model's ID (convenience for callers that only know the
// configured model name).
func TestResolveRerankModel_NameUniqueMatch(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.ResolveRerankModel(context.Background(), "rerank-b")
	require.NoError(t, err)
	assert.Equal(t, "rerank-2", got)
}

// A name shared by more than one active rerank model is ambiguous and
// fails with 400: silently picking one would be non-deterministic.
func TestResolveRerankModel_NameAmbiguous_400(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: []*types.Model{
		rerankModel("rerank-1", "shared", true),
		rerankModel("rerank-2", "shared", true),
	}})

	_, err := svc.ResolveRerankModel(context.Background(), "shared")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches multiple models")
}

// An unknown ID or name fails with 403: hard fail, no silent fallback to
// the tenant default.
func TestResolveRerankModel_NotFound_403(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	_, err := svc.ResolveRerankModel(context.Background(), "no-such-model")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

// Non-active models are invisible to resolution: unreachable by ID or
// name, and never auto-selected.
func TestResolveRerankModel_InactiveExcluded(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: []*types.Model{
		rerankModel("rerank-inactive", "inactive-name", false),
	}})

	_, err := svc.ResolveRerankModel(context.Background(), "rerank-inactive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")

	_, err = svc.ResolveRerankModel(context.Background(), "inactive-name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

// A model-list failure propagates; the tenant-configured model path must
// not depend on ListModels succeeding.
func TestResolveRerankModel_ListError_Propagates(t *testing.T) {
	ms := &rerankResolutionModelService{listErr: assert.AnError}
	svc := newRerankResolutionService(ms)

	_, err := svc.ResolveRerankModel(context.Background(), "rerank-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

// With a non-empty requested value and no model service, resolution fails
// with an internal error rather than silently returning empty.
func TestResolveRerankModel_ModelServiceNil_InternalError(t *testing.T) {
	svc := &sessionService{}

	_, err := svc.ResolveRerankModel(context.Background(), "rerank-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model service not available")
}

// --- resolveRerankModelID (full resolution chain) ---
//
// Mirrors resolveChatModelID precedence:
// request override > agent config > tenant RetrievalConfig > auto-detect.
// Request and agent values are validated hard (400/403); the tenant value
// is used verbatim (matching the retrieve API path); auto-detect picks the
// first active rerank model.
//
// Structural rule: applyAgentOverridesToChatManage must NOT overwrite
// RerankModelID; the agent step lives in this chain (see
// TestApplyAgentOverrides_RerankModelIDUntouched).

// The request override wins over agent config, tenant config and
// auto-detect: identical precedence to summary_model_id in
// resolveChatModelID.
func TestResolveRerankModelID_RequestBeatsAgentAndTenant(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.resolveRerankModelID(context.Background(),
		"rerank-1", "rerank-2", &types.RetrievalConfig{RerankModelID: "rerank-3"})
	require.NoError(t, err)
	assert.Equal(t, "rerank-1", got)
}

// With no request override, the agent config beats tenant config and
// auto-detect (agent is the more specific per-session configuration).
func TestResolveRerankModelID_AgentBeatsTenant(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.resolveRerankModelID(context.Background(),
		"", "rerank-2", &types.RetrievalConfig{RerankModelID: "rerank-3"})
	require.NoError(t, err)
	assert.Equal(t, "rerank-2", got)
}

// With no request or agent override, the tenant RetrievalConfig wins and
// auto-detect is never reached (no model list call).
func TestResolveRerankModelID_TenantBeatsAutoDetect(t *testing.T) {
	ms := &rerankResolutionModelService{models: activeRerankModels()}
	svc := newRerankResolutionService(ms)

	got, err := svc.resolveRerankModelID(context.Background(), "", "", &types.RetrievalConfig{RerankModelID: "rerank-3"})
	require.NoError(t, err)
	assert.Equal(t, "rerank-3", got)
	assert.Equal(t, 0, ms.listCalls, "auto-detect must not run when a tenant model is configured")
}

// With no override at all, the first active rerank model is auto-selected,
// keeping the rerank stage functional for tenants without a configured model.
func TestResolveRerankModelID_AutoDetectLast(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	got, err := svc.resolveRerankModelID(context.Background(), "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "rerank-1", got)
}

// Auto-detect skips non-active models: the first active rerank model wins
// even if inactive models sort earlier in the list.
func TestResolveRerankModelID_AutoDetectSkipsInactive(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: []*types.Model{
		rerankModel("rerank-inactive", "inactive-name", false),
		rerankModel("rerank-1", "rerank-a", true),
	}})

	got, err := svc.resolveRerankModelID(context.Background(), "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "rerank-1", got)
}

// An invalid request-level rerank_model_id hard-fails
// (403) and does NOT fall back to the tenant config: an explicit but wrong
// request must be surfaced, not silently ignored (unlike summary_model_id).
func TestResolveRerankModelID_RequestInvalid_HardFailsNoFallback(t *testing.T) {
	ms := &rerankResolutionModelService{models: activeRerankModels()}
	svc := newRerankResolutionService(ms)

	_, err := svc.resolveRerankModelID(context.Background(),
		"no-such-model", "", &types.RetrievalConfig{RerankModelID: "rerank-3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

// An invalid agent-configured rerank model hard-fails, mirroring
// the agent model validation in resolveChatModelID:
// a misconfigured agent is a config bug to be fixed, not papered over
// by the tenant default.
func TestResolveRerankModelID_AgentInvalid_HardFails(t *testing.T) {
	svc := newRerankResolutionService(&rerankResolutionModelService{models: activeRerankModels()})

	_, err := svc.resolveRerankModelID(context.Background(),
		"", "agent-stale-model", &types.RetrievalConfig{RerankModelID: "rerank-3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

// The tenant RetrievalConfig value is used verbatim without
// existence/active validation (matching the retrieve API path). A stale
// value fails later at the rerank stage (GetRerankModel ->
// ErrGetRerankModel), not here.
func TestResolveRerankModelID_TenantConfigNotValidated(t *testing.T) {
	ms := &rerankResolutionModelService{models: activeRerankModels()}
	svc := newRerankResolutionService(ms)

	got, err := svc.resolveRerankModelID(context.Background(),
		"", "", &types.RetrievalConfig{RerankModelID: "stale-or-invalid-id"})
	require.NoError(t, err)
	assert.Equal(t, "stale-or-invalid-id", got)
	assert.Equal(t, 0, ms.listCalls, "tenant config path must not consult the model list")
}

// When auto-detect is reached and ListModels fails, the error is swallowed
// and resolution returns empty (matching the retrieve API auto-detect
// behavior). The rerank stage then skips (empty_model_id) instead of
// failing the whole request.
func TestResolveRerankModelID_AutoDetectListError_Swallowed(t *testing.T) {
	ms := &rerankResolutionModelService{listErr: assert.AnError}
	svc := newRerankResolutionService(ms)

	got, err := svc.resolveRerankModelID(context.Background(), "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// --- applyAgentOverridesToChatManage regression ---

// applyAgentOverridesToChatManage must NOT touch RerankModelID: resolution
// is owned by resolveRerankModelID so the request > agent > tenant >
// auto-detect precedence holds (agent in the blanket pass would win over an
// explicit request override, contradicting the summary_model_id semantics).
func TestApplyAgentOverrides_RerankModelIDUntouched(t *testing.T) {
	svc := &sessionService{}
	cm := &types.ChatManage{}
	cm.RerankModelID = "resolved-by-chain"
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{RerankModelID: "agent-model"}}

	svc.applyAgentOverridesToChatManage(context.Background(), agent, cm)

	assert.Equal(t, "resolved-by-chain", cm.RerankModelID,
		"applyAgentOverridesToChatManage must not override RerankModelID (resolution is owned by resolveRerankModelID)")
}
