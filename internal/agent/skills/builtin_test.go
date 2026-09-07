package skills

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuiltinInstallerDiscoveryAndIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewManager(&ManagerConfig{Enabled: true, AllowedSkills: []string{"pdf"}}, nil).
		WithTenantSource(NewTenantSkillSource([]*types.TenantSkillEntity{
			{
				Name: InstallerSkillName, Description: "shadow", Instructions: "shadow",
				Enabled: true, Status: types.SkillStatusReady,
			},
			{Name: "pdf", Enabled: true, Status: types.SkillStatusReady},
			{Name: "hidden", Enabled: true, Status: types.SkillStatusReady},
		}, nil)).WithInstaller()
	for range 2 {
		require.NoError(t, m.Reload(ctx))
		metadata := m.GetAllMetadata()
		require.Len(t, metadata, 2)
		require.Equal(t, InstallerSkillName, metadata[1].Name)
		skill, err := m.LoadSkill(ctx, InstallerSkillName)
		require.NoError(t, err)
		require.NoError(t, skill.Validate())
		require.NotEqual(t, "shadow", skill.Instructions)
		_, err = m.LoadSkill(ctx, "hidden")
		require.Error(t, err)
		_, err = m.ReadSkillFile(ctx, InstallerSkillName, "../../secret")
		require.Error(t, err)
		_, _, err = m.PrepareShellEnvironment(ctx, "session", InstallerSkillName, "id", nil)
		require.ErrorContains(t, err, "platform tools")
	}
}

func TestBuiltinInstallerWorksWithoutInstalledSkills(t *testing.T) {
	ctx := context.Background()
	m := NewManager(&ManagerConfig{Enabled: true}, nil).WithInstaller()
	require.NoError(t, m.Initialize(ctx))
	require.Len(t, m.GetAllMetadata(), 1)
	_, err := m.LoadSkill(ctx, InstallerSkillName)
	require.NoError(t, err)
	disabled := NewManager(&ManagerConfig{Enabled: false}, nil).WithInstaller()
	require.NoError(t, disabled.Initialize(ctx))
	require.Empty(t, disabled.GetAllMetadata())
	_, err = disabled.LoadSkill(ctx, InstallerSkillName)
	require.Error(t, err)
}
