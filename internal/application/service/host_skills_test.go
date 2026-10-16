package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestHostSkillsAvailableNeedsEveryPart(t *testing.T) {
	host := &capableManager{typ: sandbox.SandboxTypeHost}
	full := HostSandboxManager{
		Manager: host, Desktop: true,
		SkillTree: &fakeHostSkillTree{}, SkillInstaller: &fakeHostSkillInstaller{},
	}
	require.True(t, full.SkillsAvailable())

	for name, h := range map[string]HostSandboxManager{
		"web":          {Manager: host, SkillTree: &fakeHostSkillTree{}, SkillInstaller: &fakeHostSkillInstaller{}},
		"no backend":   {Desktop: true, SkillTree: &fakeHostSkillTree{}, SkillInstaller: &fakeHostSkillInstaller{}},
		"no tree":      {Manager: host, Desktop: true, SkillInstaller: &fakeHostSkillInstaller{}},
		"no installer": {Manager: host, Desktop: true, SkillTree: &fakeHostSkillTree{}},
	} {
		require.False(t, h.SkillsAvailable(), name)
	}
}
