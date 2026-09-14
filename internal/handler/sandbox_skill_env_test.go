package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// skillWithEnvDeclaration is a row carrying everything the projection has to
// keep out of a response: the stored credential, the SKILL.md body and the
// pointer to the uploaded archive.
func skillWithEnvDeclaration() *types.TenantSkillEntity {
	return &types.TenantSkillEntity{
		ID: "skill-1", TenantID: testSkillTenantID, SandboxConfigID: "cfg-a",
		Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		Instructions: "run scripts/extract.py to read a pdf",
		BundleRef:    "tenant/42/skills/pdf.zip",
		Envs: types.SkillEnvVars{
			{Name: "API_TOKEN", Description: "the workspace token", Required: true, Value: "admin-secret"},
			{Name: "REGION", Description: "which region"},
		},
	}
}

// The response says whether a value exists and never what it is. Instructions
// and the bundle pointer are level-2 disclosure for the agent, not for a
// listing.
func TestSkillResponseReportsIsSetWithoutTheValue(t *testing.T) {
	raw, err := json.Marshal(toSkillResponse(skillWithEnvDeclaration()))
	require.NoError(t, err)
	body := string(raw)

	require.NotContains(t, body, "admin-secret")
	require.NotContains(t, body, "scripts/extract.py")
	require.NotContains(t, body, "pdf.zip")
	require.NotContains(t, body, `"value"`)

	require.Contains(t, body, `"API_TOKEN"`)
	require.Contains(t, body, "the workspace token")
	require.Contains(t, body, `"is_set":true`)
	require.Contains(t, body, `"required":true`)
	require.Contains(t, body, `"is_set":false`)
}

func TestSkillResponsePatchWritesEnvValues(t *testing.T) {
	skill := skillWithEnvDeclaration()
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{"skill-1": skill}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{"envs":{"API_TOKEN":"rotated"}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, map[string]string{"API_TOKEN": "rotated"}, svc.patchEnvs)
	require.NotContains(t, w.Body.String(), "rotated")
	require.True(t, svc.skills["skill-1"].Enabled, "envs-only patch must not touch visibility")
}

// An empty object is a real request — "clear everything you sent me" — and is
// distinguishable from a body that never mentioned envs at all.
func TestSkillResponsePatchAcceptsAnEmptyEnvsObject(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": skillWithEnvDeclaration(),
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{"envs":{}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.patchEnvs)
	require.Empty(t, svc.patchEnvs)
}

// The existing contract must survive: an enabled-only body is still a valid
// request and must not start requiring envs.
func TestSkillResponsePatchStillAcceptsEnabledAlone(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": skillWithEnvDeclaration(),
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, svc.patchEnabled)
	require.Nil(t, svc.patchEnvs, "an enabled-only body must not be read as clearing values")
}

func TestSkillResponsePatchAcceptsEnabledAndEnvsTogether(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": skillWithEnvDeclaration(),
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{"enabled":false,"envs":{"REGION":"eu"}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, svc.patchEnabled)
	require.Equal(t, map[string]string{"REGION": "eu"}, svc.patchEnvs)
	require.Equal(t, 1, svc.patchCalls,
		"both fields must reach the service in one call, or a failed second write "+
			"leaves the first already persisted")
}
