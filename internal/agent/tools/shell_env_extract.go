package tools

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/utils"
)

func extractExportedEnv(command string) map[string]string {
	return utils.ExtractShellAssignments(command)
}

func collectUsedSkillEnv(command string, toolEnv map[string]string) map[string]string {
	out := extractExportedEnv(command)
	for name, value := range toolEnv {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[name] = value
	}
	return out
}

func maskCommandAssignments(command string) string {
	return utils.MaskCommandAssignments(command)
}
