package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	testEnvTenantID      uint64 = 7
	testEnvConfigID      string = "cfg-1"
	testEnvOtherConfigID string = "cfg-2"
)

// fakeUserEnvReader answers per (principal, config, skill) exactly as the
// repository does: it scopes on tenant, normalised principal, config AND skill,
// so a resolver that forgot any part of the key gets nothing back rather than
// somebody else's row.
type fakeUserEnvReader struct {
	tenantID uint64
	rows     map[string][]*types.TenantUserEnvVar
	err      error

	calls int
}

func (f *fakeUserEnvReader) key(p types.Principal, configID, skillID string) string {
	p = p.Normalize()
	return p.Type + "\x00" + p.ID + "\x00" + configID + "\x00" + skillID
}

func (f *fakeUserEnvReader) put(
	p types.Principal, configID, skillID string, values map[string]string,
) {
	if f.rows == nil {
		f.rows = map[string][]*types.TenantUserEnvVar{}
	}
	key := f.key(p, configID, skillID)
	for name, value := range values {
		f.rows[key] = append(f.rows[key], &types.TenantUserEnvVar{
			TenantID: f.tenantID, PrincipalType: p.Type, PrincipalID: p.ID,
			SandboxConfigID: configID, SkillID: skillID, Name: name, Value: value,
		})
	}
}

func (f *fakeUserEnvReader) ListUserEnvVars(
	_ context.Context, tenantID uint64, p types.Principal, configID, skillID string,
) ([]*types.TenantUserEnvVar, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if tenantID != f.tenantID {
		return nil, nil
	}
	return f.rows[f.key(p, configID, skillID)], nil
}

func webPrincipal(id string) types.Principal {
	return types.Principal{Type: types.PrincipalWebUser, ID: id}
}

func imPrincipal(id string) types.Principal {
	return types.Principal{Type: types.PrincipalIMUser, ID: id}
}

func searchSkillRow() *types.TenantSkillEntity {
	return &types.TenantSkillEntity{
		ID: "sk-1", TenantID: testEnvTenantID, SandboxConfigID: testEnvConfigID,
		Name: "web-search",
		Envs: types.SkillEnvVars{
			{Name: "TAVILY_API_KEY", Required: true, Value: "workspace-key"},
			{Name: "SEARCH_REGION", Value: "workspace-region"},
			{Name: "OPTIONAL_TOKEN"},
		},
	}
}

func newTestResolver(reader userEnvReader, rows ...*types.TenantSkillEntity) *userEnvResolver {
	return NewUserEnvResolver(rows, reader, testEnvTenantID, testEnvConfigID)
}

func TestResolverUserValueOverridesWorkspaceValue(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "u1-key"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.Empty(t, missing)
	require.Equal(t, "u1-key", env["TAVILY_API_KEY"])
	require.Equal(t, "workspace-region", env["SEARCH_REGION"],
		"a name the caller did not fill in keeps the workspace value")
}

// Config-wide variables are the whole point of the second scope: they apply
// whether or not the execution names a skill.
func TestResolverInjectsConfigWideValuesWithoutASkill(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"HTTP_PROXY": "http://proxy:8080"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "")

	require.NoError(t, err)
	require.Empty(t, missing, "no skill was named, so nothing can be missing")
	require.Equal(t, map[string]string{"HTTP_PROXY": "http://proxy:8080"}, env)
}

func TestResolverAddsConfigWideValuesToASkillRun(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"HTTP_PROXY": "http://proxy:8080"})
	reader.put(webPrincipal("u1"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "u1-key"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, _, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.Equal(t, "http://proxy:8080", env["HTTP_PROXY"])
	require.Equal(t, "u1-key", env["TAVILY_API_KEY"])
}

// Specificity decides a collision between the caller's own two scopes: the
// value they entered for this skill is the more deliberate statement.
func TestResolverSkillScopeBeatsConfigWideScope(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"TAVILY_API_KEY": "config-wide"})
	reader.put(webPrincipal("u1"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "skill-scoped"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, _, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.Equal(t, "skill-scoped", env["TAVILY_API_KEY"])
}

// The caller's own config-wide value still beats the admin's workspace value:
// mine always win over the workspace's.
func TestResolverConfigWideValueBeatsWorkspaceValue(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"SEARCH_REGION": "mine"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, _, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.Equal(t, "mine", env["SEARCH_REGION"])
}

func TestResolverGivesAnotherPrincipalTheWorkspaceValue(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "u1-key"})
	resolver := newTestResolver(reader, searchSkillRow())

	env, _, err := resolver.ResolveEnv(
		types.WithPrincipal(context.Background(), webPrincipal("u2")), "web-search")

	require.NoError(t, err)
	require.Equal(t, "workspace-key", env["TAVILY_API_KEY"])
}

// The IM path sets a real IM principal AND a synthetic web user id. Keying on
// the user id instead of the principal would hand the IM caller the web user's
// key, so both directions are asserted.
func TestResolverDoesNotCrossIMAndWebPrincipals(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("system-7"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "web-key"})
	reader.put(imPrincipal("wecom:ch1:alice"), testEnvConfigID, "sk-1",
		map[string]string{"TAVILY_API_KEY": "im-key"})
	resolver := newTestResolver(reader, searchSkillRow())

	imCtx := types.WithPrincipal(context.Background(), imPrincipal("wecom:ch1:alice"))
	imCtx = context.WithValue(imCtx, types.UserIDContextKey, "system-7")
	imEnv, _, err := resolver.ResolveEnv(imCtx, "web-search")
	require.NoError(t, err)
	require.Equal(t, "im-key", imEnv["TAVILY_API_KEY"])

	webCtx := context.WithValue(context.Background(), types.UserIDContextKey, "system-7")
	webEnv, _, err := resolver.ResolveEnv(webCtx, "web-search")
	require.NoError(t, err)
	require.Equal(t, "web-key", webEnv["TAVILY_API_KEY"])
}

// A value entered on another config must not leak into this run: the resolver
// is pinned to the config the rows came from.
func TestResolverIsPinnedToItsOwnConfig(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvOtherConfigID, "",
		map[string]string{"HTTP_PROXY": "other-config"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, _, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.NotContains(t, env, "HTTP_PROXY")
}

func TestResolverReportsMissingRequiredOnly(t *testing.T) {
	row := searchSkillRow()
	row.Envs = types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Required: true},
		{Name: "OPTIONAL_TOKEN"},
	}
	resolver := newTestResolver(&fakeUserEnvReader{tenantID: testEnvTenantID}, row)
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.Equal(t, []string{"TAVILY_API_KEY"}, missing)
	require.Empty(t, env)
}

// An empty workspace value is not a value: injecting it would make an
// unconfigured variable indistinguishable from one deliberately set to "".
func TestResolverTreatsAnEmptyWorkspaceValueAsUnset(t *testing.T) {
	row := searchSkillRow()
	row.Envs = types.SkillEnvVars{{Name: "TAVILY_API_KEY", Required: true, Value: ""}}
	resolver := newTestResolver(&fakeUserEnvReader{tenantID: testEnvTenantID}, row)
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "web-search")

	require.NoError(t, err)
	require.NotContains(t, env, "TAVILY_API_KEY")
	require.Equal(t, []string{"TAVILY_API_KEY"}, missing)
}

// Never degrade to the workspace value on a lookup failure: running a skill
// with somebody else's key is worse than not running it.
func TestResolverFailsClosedWhenTheLookupFails(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID, err: errors.New("db down")}
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "web-search")

	require.Error(t, err)
	require.Nil(t, env)
	require.Nil(t, missing)
}

// Without a principal there is nobody to look up, and no lookup must be
// attempted under a fallback identity.
func TestResolverWithoutAPrincipalUsesWorkspaceValuesOnly(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	resolver := newTestResolver(reader, searchSkillRow())

	env, _, err := resolver.ResolveEnv(context.Background(), "web-search")

	require.NoError(t, err)
	require.Equal(t, "workspace-key", env["TAVILY_API_KEY"])
	require.Zero(t, reader.calls)
}

// Preloaded skills reach the resolver too and carry no declaration, so an
// unknown name is not an error — it just resolves the config-wide scope.
func TestResolverUnknownSkillStillGetsConfigWideValues(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"HTTP_PROXY": "http://proxy:8080"})
	resolver := newTestResolver(reader, searchSkillRow())
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, missing, err := resolver.ResolveEnv(ctx, "preloaded-thing")

	require.NoError(t, err)
	require.Empty(t, missing)
	require.Equal(t, map[string]string{"HTTP_PROXY": "http://proxy:8080"}, env)
}

// The resolver is built even for a run with no installed skills, so shell_exec
// still receives the caller's config-wide variables.
func TestResolverWorksWithNoInstalledSkills(t *testing.T) {
	reader := &fakeUserEnvReader{tenantID: testEnvTenantID}
	reader.put(webPrincipal("u1"), testEnvConfigID, "",
		map[string]string{"HTTP_PROXY": "http://proxy:8080"})
	resolver := NewUserEnvResolver(nil, reader, testEnvTenantID, testEnvConfigID)
	ctx := types.WithPrincipal(context.Background(), webPrincipal("u1"))

	env, _, err := resolver.ResolveEnv(ctx, "")

	require.NoError(t, err)
	require.Equal(t, "http://proxy:8080", env["HTTP_PROXY"])
}
