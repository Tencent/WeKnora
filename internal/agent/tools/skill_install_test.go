package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type fakeSkillInstaller struct {
	calls          int
	tenant         uint64
	config, source string
	row            *types.TenantSkillEntity
	readErr        error
}

type skillAttachmentFiles struct {
	SandboxFileSource
	stat          *sandbox.RemoteStatEntry
	reads         int
	session, path string
}

func (f *skillAttachmentFiles) StatSessionFile(
	_ context.Context, session, path string,
) (*sandbox.RemoteStatEntry, error) {
	f.session, f.path = session, path
	return f.stat, nil
}

func (f *skillAttachmentFiles) ReadSessionFile(_ context.Context, session, path string) ([]byte, error) {
	f.session, f.path = session, path
	f.reads++
	return []byte("zip-bytes"), nil
}

func TestInstallSkillFromChatZIPIsSessionScoped(t *testing.T) {
	ctx := WithToolExecContext(installTestContext(types.TenantRoleAdmin), &ToolExecContext{SessionID: "own-session"})
	f := &fakeSkillInstaller{}
	files := &skillAttachmentFiles{stat: &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: 9}}
	tool := NewInstallSkillTool(f, 7, "cfg").WithAttachments(files)
	result, err := tool.Execute(ctx, json.RawMessage(`{"archive_path":"/workspace/input/attachment/skill.zip"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "own-session", files.session)
	require.Equal(t, "zip-bytes", f.source)
	require.Equal(t, 1, f.calls)
	for _, archivePath := range []string{
		"/etc/secret.zip", "/workspace/input/../secret.zip", "/workspace/input/skill.txt", "skill.zip",
	} {
		args, _ := json.Marshal(map[string]string{"archive_path": archivePath})
		result, err = tool.Execute(ctx, args)
		require.NoError(t, err)
		require.False(t, result.Success)
	}
	files.stat.Size = utils.GetMaxSkillBundleSize() + 1
	result, err = tool.Execute(ctx, json.RawMessage(`{"archive_path":"/workspace/input/too-big.zip"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	files.stat.Type = sandbox.RemoteEntryOther
	result, err = tool.Execute(ctx, json.RawMessage(`{"archive_path":"/workspace/input/link.zip"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, files.reads)
	require.Equal(t, 1, f.calls)
}

func (f *fakeSkillInstaller) InstallSkill(
	_ context.Context, tenant uint64, config string, archive []byte,
) (string, error) {
	f.calls++
	f.tenant, f.config, f.source = tenant, config, string(archive)
	return "skill-1", nil
}

func (f *fakeSkillInstaller) InstallSkillFromSource(
	_ context.Context, tenant uint64, config, source string,
) (string, error) {
	f.calls++
	f.tenant, f.config, f.source = tenant, config, source
	return "skill-1", nil
}

func (f *fakeSkillInstaller) GetSkill(
	_ context.Context, tenant uint64, config, _ string,
) (*types.TenantSkillEntity, error) {
	f.tenant, f.config = tenant, config
	return f.row, f.readErr
}

func installTestContext(role types.TenantRole) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return context.WithValue(ctx, types.TenantRoleContextKey, role)
}

func TestInstallSkillPermissionsAndScope(t *testing.T) {
	admin := installTestContext(types.TenantRoleAdmin)
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		allowed bool
	}{
		{"admin", admin, true},
		{"owner", installTestContext(types.TenantRoleOwner), true},
		{"viewer", installTestContext(types.TenantRoleViewer), false},
		{"contributor", installTestContext(types.TenantRoleContributor), false},
		{"missing role", context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)), false},
		{"different tenant", context.WithValue(admin, types.TenantIDContextKey, uint64(8)), false},
		{
			"restricted key with owner role",
			types.WithTenantAPIKeyScope(installTestContext(types.TenantRoleOwner), types.TenantAPIKeyScope{}), false,
		},
		{
			"full access key", types.WithTenantAPIKeyScope(installTestContext(types.TenantRoleViewer),
				types.TenantAPIKeyScope{FullAccess: true}), true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSkillInstaller{}
			tool := NewInstallSkillTool(f, 7, "pinned-config")
			result, err := tool.Execute(tc.ctx, json.RawMessage(`{"source":"@owner/pdf"}`))
			require.NoError(t, err)
			require.Equal(t, tc.allowed, result.Success)
			if tc.allowed {
				require.Equal(t, 1, f.calls)
				require.Equal(t, uint64(7), f.tenant)
				require.Equal(t, "pinned-config", f.config)
				require.Equal(t, "@owner/pdf", f.source)
				require.Equal(t, "accepted", result.Data["status"])
			} else {
				require.Zero(t, f.calls)
			}
		})
	}
}

func TestInstallSkillStatusDoesNotExposeEntityOrRepeatMutation(t *testing.T) {
	ctx := installTestContext(types.TenantRoleAdmin)
	f := &fakeSkillInstaller{readErr: errors.New("temporary status failure")}
	tool := NewInstallSkillTool(f, 7, "cfg")
	result, err := tool.Execute(ctx, json.RawMessage(`{"source":"https://github.com/owner/repo"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "accepted", result.Data["status"])
	f.readErr = nil
	f.row = &types.TenantSkillEntity{Name: "pdf", Status: types.SkillStatusReady, BundleRef: "private-storage-ref"}
	result, err = tool.Execute(ctx, json.RawMessage(`{"action":"status","skill_id":"skill-1"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, types.SkillStatusReady, result.Data["status"])
	require.NotContains(t, result.Output, "private-storage-ref")
	require.Equal(t, 1, f.calls)
	for _, args := range []string{`{}`, `{"action":"delete"}`, `{"action":"status"}`, `{"source":"x","skill_id":"y"}`} {
		result, err = tool.Execute(ctx, json.RawMessage(args))
		require.NoError(t, err)
		require.False(t, result.Success)
	}
	require.Equal(t, 1, f.calls)
}

func TestReadBuiltinInstallerProvidesPlatformExecutionGuidance(t *testing.T) {
	m := skills.NewManager(&skills.ManagerConfig{Enabled: true}, nil).WithInstaller()
	require.NoError(t, m.Initialize(context.Background()))
	reader := NewReadFileTool(nil).WithSkills(m, true)
	result, err := reader.Execute(context.Background(), json.RawMessage(`{"path":"skill://skill-installer/SKILL.md"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, "platform tools")
	require.NotContains(t, result.Output, "Execution: shell_exec")
}
