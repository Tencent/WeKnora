package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecuteOnHandleRunsRemoteScriptWithoutUploading(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	skillDir := mustSkillDir(t, "sk-1")
	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: skillDir + "/scripts/run.py",
		Args:             []string{"--flag"},
	})

	require.NoError(t, err)
	client.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Empty(t, writes,
		"a script that already lives in the image must not be re-uploaded")
	require.Len(t, execs, 1)
	last := execs[0]
	require.Equal(t, SessionWorkspaceRoot, last.WorkDir)
	require.Equal(t, DefaultSandboxExecUser, last.User)
	require.Contains(t, last.Args[len(last.Args)-1], "--flag")
	require.Contains(t, last.Args[1], skillDir+"/.venv/bin/python")
}

func TestExecuteOnHandleNestedRemoteScriptUsesSkillRootVenv(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	skillDir := mustSkillDir(t, "sk-1")
	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: skillDir + "/scripts/tools/run.py",
	})

	require.NoError(t, err)
	client.mu.Lock()
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Len(t, execs, 1)
	require.Contains(t, execs[0].Args[1], skillDir+"/.venv/bin/python")
}

func TestExecuteOnHandleRejectsRemoteScriptOutsideSkillRoot(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: "/workspace/run.py",
	})

	require.ErrorIs(t, err, ErrInvalidScript)
	client.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Empty(t, writes)
	require.Empty(t, execs)
}

func TestExecuteOnHandleRunsWorkspaceScriptWithExplicitSkillDir(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	skillDir := mustSkillDir(t, "ppt-generator")
	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: "/workspace/output/generate_rencui_ppt.py",
		SkillDir:         skillDir,
		Args:             []string{"--flag"},
	})

	require.NoError(t, err)
	client.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), client.writeFiles...)
	execs := append([]RemoteExecRequest(nil), client.execRequests...)
	client.mu.Unlock()
	require.Empty(t, writes, "a session workspace script must not be uploaded")
	require.Len(t, execs, 1)
	require.Equal(t, SessionWorkspaceRoot, execs[0].WorkDir)
	require.Contains(t, execs[0].Args[1], skillDir+"/.venv/bin/python")
	require.Contains(t, execs[0].Args[1], "/workspace/output/generate_rencui_ppt.py")
	require.Contains(t, execs[0].Args[len(execs[0].Args)-1], "--flag")
}

func TestExecuteOnHandleRejectsWorkspaceScriptWithoutSkillDir(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: "/workspace/output/generate_ppt.py",
	})
	require.ErrorIs(t, err, ErrInvalidScript)
}

func TestExecuteOnHandleRejectsWorkspaceInputEvenWithSkillDir(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)
	sb := NewRemoteSandbox(client, RemoteCreateRequest{})
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "tpl"})
	require.NoError(t, err)

	_, err = sb.ExecuteOnHandle(ctx, handle, &ExecuteConfig{
		RemoteScriptPath: "/workspace/input/upload.py",
		SkillDir:         mustSkillDir(t, "pdf"),
	})
	require.ErrorIs(t, err, ErrInvalidScript)
	client.mu.Lock()
	require.Empty(t, client.execRequests)
	client.mu.Unlock()
}
