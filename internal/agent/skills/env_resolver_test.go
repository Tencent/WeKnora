package skills

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The runtime seeds the artifact directory variables before the resolver runs.
// Whatever a user stored, those keys must survive untouched: they are how the
// turn's products are collected.
func TestApplyResolvedEnvAddsNewKeysAndKeepsExistingOnes(t *testing.T) {
	env := map[string]string{
		artifactOutputEnvVar:  "/workspace/output",
		artifactHistoryEnvVar: "/workspace/output",
	}

	applyResolvedEnv(env, map[string]string{
		"TAVILY_API_KEY":      "user-key",
		artifactOutputEnvVar:  "/tmp/hijacked",
		artifactHistoryEnvVar: "/tmp/hijacked",
	})

	require.Equal(t, "user-key", env["TAVILY_API_KEY"])
	require.Equal(t, "/workspace/output", env[artifactOutputEnvVar])
	require.Equal(t, "/workspace/output", env[artifactHistoryEnvVar])
}

func TestApplyResolvedEnvToleratesNilResolved(t *testing.T) {
	env := map[string]string{artifactOutputEnvVar: "/workspace/output"}

	applyResolvedEnv(env, nil)

	require.Equal(t, map[string]string{artifactOutputEnvVar: "/workspace/output"}, env)
}

// The message is read by the agent and relayed verbatim to a person, so it has
// to name the skill, the variables, and where to go and fix it.
func TestMissingSkillEnvErrorMessageNamesSkillVarsAndWhereToSetThem(t *testing.T) {
	err := error(&MissingSkillEnvError{
		SkillName: "web-search",
		Names:     []string{"TAVILY_API_KEY", "SERP_TOKEN"},
	})

	msg := err.Error()
	require.Contains(t, msg, "web-search")
	require.Contains(t, msg, "TAVILY_API_KEY")
	require.Contains(t, msg, "SERP_TOKEN")
	require.Contains(t, msg, "Environment variables")

	var typed *MissingSkillEnvError
	require.True(t, errors.As(err, &typed))
	require.Equal(t, "web-search", typed.SkillName)
	require.Equal(t, []string{"TAVILY_API_KEY", "SERP_TOKEN"}, typed.Names)
}

// stubEnvResolver records what ExecuteScript asked it for.
type stubEnvResolver struct {
	env     map[string]string
	missing []string
	err     error

	calls []string
}

func (r *stubEnvResolver) ResolveEnv(
	_ context.Context, skillName string,
) (map[string]string, []string, error) {
	r.calls = append(r.calls, skillName)
	return r.env, r.missing, r.err
}

func TestExecuteScriptBlocksOnMissingRequiredEnv(t *testing.T) {
	dir := preloadedSkillDir(t, "web-search", "search the web")
	sandboxMgr := &recordingSandboxManager{}
	resolver := &stubEnvResolver{missing: []string{"TAVILY_API_KEY"}}
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, sandboxMgr).
		WithEnvResolver(resolver)
	require.NoError(t, mgr.Initialize(context.Background()))

	result, err := mgr.ExecuteScript(context.Background(), "web-search", "scripts/run.py", nil, "")

	require.Nil(t, result)
	var missing *MissingSkillEnvError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "web-search", missing.SkillName)
	require.Equal(t, []string{"TAVILY_API_KEY"}, missing.Names)
	// The whole point of blocking is that the script never runs.
	require.Zero(t, sandboxMgr.calls)
	require.Nil(t, sandboxMgr.config)
}

func TestExecuteScriptPropagatesResolverError(t *testing.T) {
	dir := preloadedSkillDir(t, "web-search", "search the web")
	sandboxMgr := &recordingSandboxManager{}
	boom := errors.New("load user envs: connection refused")
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, sandboxMgr).
		WithEnvResolver(&stubEnvResolver{err: boom})
	require.NoError(t, mgr.Initialize(context.Background()))

	_, err := mgr.ExecuteScript(context.Background(), "web-search", "scripts/run.py", nil, "")

	require.ErrorIs(t, err, boom)
	require.Zero(t, sandboxMgr.calls)
}

func TestExecuteScriptInjectsResolvedEnvOnceKeyedBySkillName(t *testing.T) {
	dir := preloadedSkillDir(t, "web-search", "search the web")
	sandboxMgr := &recordingSandboxManager{}
	resolver := &stubEnvResolver{env: map[string]string{
		"TAVILY_API_KEY":     "user-key",
		artifactOutputEnvVar: "/tmp/hijacked",
	}}
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, sandboxMgr).
		WithEnvResolver(resolver)
	require.NoError(t, mgr.Initialize(context.Background()))

	_, err := mgr.ExecuteScript(context.Background(), "web-search", "scripts/run.py", nil, "")

	require.NoError(t, err)
	require.Equal(t, []string{"web-search"}, resolver.calls)
	require.Equal(t, 1, sandboxMgr.calls)
	require.NotNil(t, sandboxMgr.config)
	require.Equal(t, "user-key", sandboxMgr.config.Env["TAVILY_API_KEY"])
	require.Equal(t, "/workspace/output", sandboxMgr.config.Env[artifactOutputEnvVar])
}

// Without a resolver the execution path must stay exactly as it was.
func TestExecuteScriptWithoutResolverInjectsNothingExtra(t *testing.T) {
	dir := preloadedSkillDir(t, "web-search", "search the web")
	sandboxMgr := &recordingSandboxManager{}
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, sandboxMgr)
	require.NoError(t, mgr.Initialize(context.Background()))

	_, err := mgr.ExecuteScript(context.Background(), "web-search", "scripts/run.py", nil, "")

	require.NoError(t, err)
	require.Equal(t, 1, sandboxMgr.calls)
	injected := map[string]bool{}
	for _, name := range InjectedSandboxEnvVars() {
		injected[name] = true
	}
	for key := range sandboxMgr.config.Env {
		require.True(t, injected[key], "injected env %q is not in InjectedSandboxEnvVars", key)
	}
}
