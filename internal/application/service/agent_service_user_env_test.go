package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Without a sandbox config there is nothing to resolve against: no config-wide
// scope to read and no installed skill to name.
func TestAgentServiceUserEnvResolverNilWithoutASandboxConfig(t *testing.T) {
	s := &agentService{db: &gorm.DB{}}

	require.Nil(t, s.userEnvResolver(context.Background(), &types.AgentConfig{}))
}

func TestAgentServiceUserEnvResolverNilWithoutDB(t *testing.T) {
	s := &agentService{}

	resolver := s.userEnvResolver(context.Background(), &types.AgentConfig{
		SandboxConfigID: "cfg-1",
		TenantSkills:    []*types.TenantSkillEntity{{ID: "sk-1", TenantID: 7, Name: "web-search"}},
	})

	require.Nil(t, resolver)
}

// The resolver exists even with no installed skills, so shell_exec still gets
// the caller's config-wide variables.
func TestAgentServiceUserEnvResolverBuiltWithoutInstalledSkills(t *testing.T) {
	s := &agentService{db: &gorm.DB{}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	resolver := s.userEnvResolver(ctx, &types.AgentConfig{SandboxConfigID: "cfg-1"})

	require.NotNil(t, resolver)
	typed, ok := resolver.(*userEnvResolver)
	require.True(t, ok)
	require.Equal(t, uint64(7), typed.tenantID)
	require.Equal(t, "cfg-1", typed.configID)
	require.Empty(t, typed.byName)
}

// The tenant id is read off the row, matching tenantSkillSource, so the lookup
// cannot resolve into a different workspace.
func TestAgentServiceUserEnvResolverIndexesByNameAndRowTenant(t *testing.T) {
	s := &agentService{db: &gorm.DB{}}

	resolver := s.userEnvResolver(context.Background(), &types.AgentConfig{
		SandboxConfigID: "cfg-1",
		TenantSkills: []*types.TenantSkillEntity{
			{ID: "sk-1", TenantID: 7, Name: "web-search"},
			{ID: "sk-2", TenantID: 7, Name: "pdf"},
		},
	})

	require.NotNil(t, resolver)
	typed, ok := resolver.(*userEnvResolver)
	require.True(t, ok)
	require.Equal(t, uint64(7), typed.tenantID)
	require.Equal(t, "cfg-1", typed.configID)
	require.Contains(t, typed.byName, "web-search")
	require.Contains(t, typed.byName, "pdf")
	require.Equal(t, "sk-1", typed.byName["web-search"].ID)
}

func TestAgentServiceSkillEnvCaptureNilWithoutASandboxConfig(t *testing.T) {
	s := &agentService{db: &gorm.DB{}}

	require.Nil(t, s.skillEnvCapture(&types.AgentConfig{}))
	require.Nil(t, s.skillEnvCapture(nil))
}

func TestAgentServiceSkillEnvCaptureNilWithoutDB(t *testing.T) {
	s := &agentService{}

	require.Nil(t, s.skillEnvCapture(&types.AgentConfig{SandboxConfigID: "cfg-1"}))
}

func TestAgentServiceSkillEnvCaptureBuiltWithConfigAndDB(t *testing.T) {
	s := &agentService{db: &gorm.DB{}}

	require.NotNil(t, s.skillEnvCapture(&types.AgentConfig{SandboxConfigID: "cfg-1"}))
}
