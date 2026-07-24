package types

import (
	"encoding/json"
	"strings"
)

const CurrentAgentPromptProtocolVersion = 2

// UnmarshalJSON migrates the one legacy prompt field that can be preserved
// safely. Legacy protocol-bound fields are intentionally ignored: current
// runtime contracts always come from managed templates.
func (c *CustomAgentConfig) UnmarshalJSON(data []byte) error {
	type configAlias CustomAgentConfig
	var payload struct {
		*configAlias
		LegacySystemPrompt string `json:"system_prompt"`
	}
	payload.configAlias = (*configAlias)(c)
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if strings.TrimSpace(c.UserInstructions) == "" && c.SystemPromptID == "" {
		c.UserInstructions = strings.TrimSpace(payload.LegacySystemPrompt)
	}
	return nil
}
