package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
)

func TestEverySelectableToolHasAFactory(t *testing.T) {
	names := []string{tools.ToolWebSearch, tools.ToolWebFetch, tools.ToolSearchMemory}
	for _, def := range tools.AvailableToolDefinitions() {
		names = append(names, def.Name)
	}
	names = append(names, tools.DefaultAllowedTools()...)
	for _, name := range names {
		if _, ok := builtinToolFactories[name]; !ok {
			t.Errorf("%s has no factory in builtinToolFactories", name)
		}
		if sandboxBoundTools[name] {
			t.Errorf("%s is both selectable and sandbox-bound", name)
		}
	}
}

func TestFactoriesAndSandboxToolsDoNotOverlap(t *testing.T) {
	for name := range sandboxBoundTools {
		if _, ok := builtinToolFactories[name]; ok {
			t.Errorf("%s is sandbox-bound but also has a factory", name)
		}
	}
}
