package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
)

type shellCapableSandboxManager struct {
	typ sandbox.SandboxType
}

func (m *shellCapableSandboxManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return &sandbox.ExecuteResult{}, nil
}
func (m *shellCapableSandboxManager) Cleanup(context.Context) error { return nil }
func (m *shellCapableSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *shellCapableSandboxManager) GetType() sandbox.SandboxType  { return m.typ }
func (m *shellCapableSandboxManager) ExecShellCommand(
	context.Context,
	string,
	string,
	string,
	time.Duration,
	map[string]string,
) (*sandbox.ExecuteResult, error) {
	return &sandbox.ExecuteResult{}, nil
}

func TestCubeShellExecutorRejectsNonCubeBackends(t *testing.T) {
	for _, typ := range []sandbox.SandboxType{
		sandbox.SandboxTypeLocal,
		sandbox.SandboxTypeDocker,
		sandbox.SandboxTypeDisabled,
	} {
		executor, ok := cubeShellExecutor(&shellCapableSandboxManager{typ: typ})
		assert.False(t, ok, "backend %s must not expose shell_exec", typ)
		assert.Nil(t, executor)
	}

	executor, ok := cubeShellExecutor(&shellCapableSandboxManager{typ: sandbox.SandboxTypeCube})
	assert.True(t, ok)
	assert.NotNil(t, executor)
}
