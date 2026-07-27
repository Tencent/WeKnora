package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/stretchr/testify/assert"
)

func TestFormatSkillsMetadataExplainsOfflineFallback(t *testing.T) {
	prompt := formatSkillsMetadata([]*skills.SkillMetadata{
		{Name: "case-lookup", Description: "Analyze legal disputes and look up cases when sources are available."},
	})

	assert.Contains(t, prompt, "Skills provide workflows and procedures; selecting a skill does not bind or create a knowledge base")
	assert.Contains(t, prompt, "Do not refuse solely because no knowledge base is bound")
	assert.Contains(t, prompt, "Never invent citations, case names, case numbers, or retrieval results")
}
