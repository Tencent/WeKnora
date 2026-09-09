package skills

import (
	"context"
	"testing"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuiltinSourceUsesPreinstalledPathsAndAgentSelection(t *testing.T) {
	source := NewBuiltinSkillSource(builtin.PublishedManifest())
	manager := NewManager(
		&ManagerConfig{Enabled: true, AllowedSkills: []string{"pdf"}},
		sandbox.NewDisabledManager(),
	).WithTenantSource(source)
	require.NoError(t, manager.Initialize(context.Background()))
	require.Len(t, manager.GetAllMetadata(), 1)
	dir, ok := manager.SandboxSkillDir("pdf")
	require.True(t, ok)
	require.Equal(t, builtin.ImageRoot+"/pdf", dir)
	_, ok = manager.SandboxSkillDir("browser")
	require.False(t, ok)
	file, err := source.LoadSkillFile("pdf", "SKILL.md")
	require.NoError(t, err)
	require.Contains(t, file.Content, "pdf")
	require.Equal(t, builtin.ImageRoot+"/pdf/SKILL.md", file.Path)
	_, err = source.LoadSkillFile("pdf", "../../browser/SKILL.md")
	require.Error(t, err)
	_, err = source.GetSkillBasePath("../pdf")
	require.Error(t, err)
	dir, ok = sandbox.InterpreterSkillDir(
		builtin.ImageRoot+"/pdf/scripts/weknora_smoke.py",
		sandbox.SkillsImageRoot+"/other",
	)
	require.True(t, ok)
	require.Equal(t, builtin.ImageRoot+"/pdf", dir)
}

func TestBuiltinSourceDoesNotUseMismatchedImageResources(t *testing.T) {
	m := builtin.PublishedManifest()
	m.Skills[0].Digest = "outdated"
	metadata, err := NewBuiltinSkillSource(m).DiscoverSkills()
	require.NoError(t, err)
	require.Len(t, metadata, 7)
	m.Skills = append(m.Skills, m.Skills[1])
	metadata, err = NewBuiltinSkillSource(m).DiscoverSkills()
	require.NoError(t, err)
	require.Empty(t, metadata, "ambiguous duplicate declarations must be refused")
	metadata, err = NewBuiltinSkillSource(nil).DiscoverSkills()
	require.NoError(t, err)
	require.Empty(t, metadata)
}

func TestInstalledSkillTakesPrecedenceOverBuiltin(t *testing.T) {
	tenant := NewTenantSkillSource(
		[]*types.TenantSkillEntity{
			{Name: "pdf", Description: "workspace version", Status: types.SkillStatusReady, Enabled: true},
		},
		nil,
	)
	source, err := MergeImageSkillSources(tenant, NewBuiltinSkillSource(builtin.PublishedManifest()))
	require.NoError(t, err)
	metadata, err := source.DiscoverSkills()
	require.NoError(t, err)
	require.Len(t, metadata, 8)
	dir, err := source.GetSkillBasePath("pdf")
	require.NoError(t, err)
	require.Equal(t, sandbox.SkillsImageRoot+"/pdf", dir)
	skill, err := source.LoadSkillInstructions("pdf")
	require.NoError(t, err)
	require.Equal(t, "workspace version", skill.Description)
}
