//go:build docker_integration

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Exercises the model-facing primitives against a real container, including
// file ownership and interpreter selection that an HTTP mock cannot prove.
func TestShellRuntimeIntegration(t *testing.T) {
	image := os.Getenv("DOCKER_INTEGRATION_IMAGE")
	if image == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE is required")
	}
	cfg := sandbox.DefaultConfig()
	cfg.Type, cfg.DockerImage = sandbox.SandboxTypeDocker, image
	client, err := sandbox.NewDockerRemoteClient(cfg)
	require.NoError(t, err)
	mgr, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: sandbox.NewMemorySessionSandboxBindingStore(),
		Checker: sandbox.PermissiveSessionExistenceChecker{}, SkipHealthProbe: true,
	})
	require.NoError(t, err)
	sessionID := fmt.Sprintf("tools-runtime-%d", time.Now().UnixNano())
	base := types.WithSandboxTenantID(context.Background(), 1)
	ctx, cancel := context.WithTimeout(base, 2*time.Minute)
	defer cancel()
	ctx = WithToolExecContext(ctx, &ToolExecContext{SessionID: sessionID})
	t.Cleanup(func() { require.NoError(t, mgr.DestroySession(base, sessionID)) })

	// A previously installed image may contain a read-only venv without pip.
	// Root sessions must still be able to use and update their own copy.
	dir := sandbox.SkillsImageRoot + "/runtime-probe"
	require.NoError(t, mgr.WriteSessionFile(ctx, sessionID, dir+"/SKILL.md", []byte("# Runtime probe\n")),
		"the installer filesystem must use the maintenance identity")
	setup := `python3 -m venv --without-pip .venv && .venv/bin/python -c 'import sysconfig,pathlib; pathlib.Path(sysconfig.get_path("purelib"),"runtime_probe.py").write_text("VALUE = 41\n")' && chmod -R a-w ` + sandbox.ShellQuote(dir)
	prepared, err := mgr.ExecShellCommandWithOptions(ctx, sessionID, setup, sandbox.ShellExecOptions{
		AsRoot: true, AllowSkillsRoot: true, WorkDir: dir, Timeout: time.Minute,
	})
	require.NoError(t, err)
	require.True(t, prepared.IsSuccess(), "%+v", prepared)
	manager := skills.NewManager(&skills.ManagerConfig{Enabled: true}, mgr)
	manager.WithTenantSource(skills.NewTenantSkillSource([]*types.TenantSkillEntity{
		{Name: "runtime-probe", Status: types.SkillStatusReady, Enabled: true},
	}, nil))
	require.NoError(t, manager.Initialize(ctx))
	shell := NewShellExecTool(mgr, nil).WithSkillEnvironment(manager)
	registry := NewToolRegistry()
	registry.RegisterTool(shell)
	registry.RegisterTool(NewWriteSandboxFileTool(mgr, 0))
	registry.RegisterTool(NewReadFileTool(mgr).WithSkills(manager, true))
	registry.RegisterTool(NewEditSandboxFileTool(mgr))
	call := func(name, args string) *types.ToolResult {
		r, err := registry.ExecuteTool(ctx, name, json.RawMessage(args))
		require.NoError(t, err)
		require.NotNil(t, r)
		return r
	}
	resource := call(ToolReadFile, `{"path":"skill://runtime-probe/SKILL.md"}`)
	require.True(t, resource.Success, "%+v", resource)
	require.Contains(t, resource.Output, `shell_exec(skill_name="runtime-probe"`)
	environment := call(ToolShellExec, `{"command":"a=(one two); printf '%s:%s' \"$HOME\" \"${#a[@]}\""}`)
	require.Equal(t, 0, environment.Data["exit_code"], "%+v", environment)
	require.Equal(t, "/root:2", environment.Data["stdout"], "shell user, HOME and Bash grammar must agree")
	r := call(ToolWriteSandboxFile, `{"path":"scripts/probe.py","content":"import runtime_probe\nprint(runtime_probe.VALUE + 1)\n"}`)
	require.True(t, r.Success, "%+v", r)
	r = call(ToolShellExec, `{"skill_name":"runtime-probe","command":"python3 /workspace/scripts/probe.py","work_dir":"output/new-dir"}`)
	require.True(t, r.Success, "%+v", r)
	require.Equal(t, 0, r.Data["exit_code"], "%+v", r)
	require.Equal(t, "42\n", r.Data["stdout"])
	r = call(ToolShellExec, `{"skill_name":"runtime-probe","command":"printf changed > \"$WEKNORA_SKILL_DIR/SKILL.md\""}`)
	require.Equal(t, 0, r.Data["exit_code"], "root sessions may update their own installed tree")
	r = call(ToolWriteSandboxFile, `{"path":"npm-package/package.json","content":"{\"name\":\"runtime-node-probe\",\"version\":\"1.0.0\",\"main\":\"index.js\"}"}`)
	require.True(t, r.Success, "%+v", r)
	r = call(ToolWriteSandboxFile, `{"path":"npm-package/index.js","content":"module.exports = 42;"}`)
	require.True(t, r.Success, "%+v", r)
	installArgs, err := json.Marshal(ShellExecInput{
		SkillName: "runtime-probe",
		Command:   strings.ReplaceAll(skillNodePackageInstallCommand, "<package>", "/workspace/npm-package --offline --ignore-scripts --no-audit --no-fund"),
	})
	require.NoError(t, err)
	r = call(ToolShellExec, string(installArgs))
	require.True(t, r.Success, "%+v", r)
	require.Equal(t, 0, r.Data["exit_code"], "%+v", r)
	r = call(ToolShellExec, `{"skill_name":"runtime-probe","command":"node -e \"console.log(require('runtime-node-probe'))\""}`)
	require.Equal(t, "42\n", r.Data["stdout"], "%+v", r)
	r = call(ToolEditSandboxFile, `{"path":"scripts/probe.py","edits":[{"old_string":"+ 1","new_string":"+ 2"}]}`)
	require.True(t, r.Success, "%+v", r)
	r = call(ToolShellExec, `{"skill_name":"runtime-probe","command":"python3 scripts/probe.py"}`)
	require.Equal(t, "43\n", r.Data["stdout"])
	r = call(ToolShellExec, `{"command":"python3 -c 'import runtime_probe'"}`)
	require.NotEqual(t, 0, r.Data["exit_code"], "skill environment must not persist into ordinary commands")
	r = call(ToolReadFile, `{"path":"scripts/probe.py"}`)
	require.True(t, r.Success, "%+v", r)
	require.Contains(t, r.Output, "+ 2")
	r = call(ToolShellExec, `{"command":"sleep 5","timeout_sec":1}`)
	require.False(t, r.Success, "timeouts must never report tool success")
	r = call(ToolWriteSandboxFile, `{"path":"input/original.txt","content":"do not overwrite"}`)
	require.False(t, r.Success)

	// A host skill must use the same executor: stage its code and binary
	// assets, preserve argv/stdin, and never reference a host filesystem path.
	hostRoot := t.TempDir()
	hostDir := filepath.Join(hostRoot, "host-probe")
	require.NoError(t, os.MkdirAll(filepath.Join(hostDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "SKILL.md"), []byte("---\nname: host-probe\ndescription: Test staging\n---\nRun scripts/run.py.\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "asset.bin"), []byte{0, 255, 1}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "scripts", "run.py"), []byte(`import os, pathlib, sys
base = pathlib.Path(os.environ["WEKNORA_SKILL_DIR"])
assert str(pathlib.Path.cwd()) == "/workspace"
print((base / "asset.bin").read_bytes().hex())
print(sys.argv[1])
sys.stdout.write(sys.stdin.read())
`), 0o644))
	hostSkills := skills.NewManager(&skills.ManagerConfig{Enabled: true, SkillDirs: []string{hostRoot}}, mgr)
	require.NoError(t, hostSkills.Initialize(ctx))
	registry = NewToolRegistry()
	registry.RegisterTool(NewShellExecTool(mgr, nil).WithSkillEnvironment(hostSkills))
	stdin := "literal $HOME and $(printf wrong)\n中文\n\n"
	args, err := json.Marshal(ShellExecInput{
		SkillName: "host-probe", Command: `python3 "$WEKNORA_SKILL_DIR/scripts/run.py" 'argument with spaces'`, Stdin: stdin,
	})
	require.NoError(t, err)
	r = call(ToolShellExec, string(args))
	require.True(t, r.Success, "%+v", r)
	require.Equal(t, 0, r.Data["exit_code"], "%+v", r)
	require.Equal(t, "00ff01\nargument with spaces\n"+stdin, r.Data["stdout"])

	// Follow the missing-environment guidance for a host skill, then ensure
	// the next named call selects the newly created staged virtualenv.
	args, err = json.Marshal(ShellExecInput{SkillName: "host-probe", Command: skillPythonVenvCreateCommand})
	require.NoError(t, err)
	r = call(ToolShellExec, string(args))
	require.Equal(t, 0, r.Data["exit_code"], "%+v", r)
	r = call(ToolShellExec, `{"skill_name":"host-probe","command":"python3 -c \"import os, sys; assert sys.prefix == os.environ['WEKNORA_SKILL_DIR'] + '/.venv'; print('staged venv selected')\""}`)
	require.Equal(t, "staged venv selected\n", r.Data["stdout"], "%+v", r)
}
