package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
)

func TestExecuteSkillScriptRecoversWhenInstructionOnlySkillHasNoScript(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	skillsDir := filepath.Join(filepath.Dir(filename), "..", "..", "..", "skills", "preloaded")
	manager := skills.NewManager(&skills.ManagerConfig{
		Enabled: true, SkillDirs: []string{skillsDir}, AllowedSkills: []string{"查案例"},
	}, nil)
	tool := NewExecuteSkillScriptTool(manager)
	args, _ := json.Marshal(ExecuteSkillScriptInput{
		SkillName: "查案例", ScriptPath: "scripts/search.py",
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.Success {
		t.Fatalf("Execute() result = %+v, error = %v", result, err)
	}
	if !strings.Contains(result.Output, "instruction-only") || !strings.Contains(result.Output, "Do not retry") {
		t.Fatalf("recovery output = %q", result.Output)
	}
	if executed, _ := result.Data["executed"].(bool); executed {
		t.Fatalf("missing script must not be reported as executed: %+v", result.Data)
	}
}
