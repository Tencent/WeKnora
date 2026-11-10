package handler

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// allowedToolsWarnings tells the caller what saving did to an allowed_tools
// list without rejecting it.
type allowedToolsWarnings struct {
	// KeptUnknown are stored names no built-in tool answers to. They stay so
	// the agent can still be saved, but the agent cannot call them.
	KeptUnknown []string `json:"kept_unknown,omitempty"`
	// Dropped are tools other agent settings decide on; listing them had no
	// effect, so they are not stored.
	Dropped []string `json:"dropped,omitempty"`
}

// checkAgentAllowedTools validates config.AllowedTools before an agent is
// saved and rewrites it to the list that will be stored. stored is the
// agent's current list, nil for a new agent. Names a request adds that no
// built-in tool answers to are rejected, so a typo cannot silently leave an
// agent without the tool it was meant to get.
func checkAgentAllowedTools(stored []string, config *types.CustomAgentConfig) (*allowedToolsWarnings, error) {
	check := tools.CheckAllowedTools(stored, config.AllowedTools)
	if len(check.Rejected) > 0 {
		return nil, errors.NewBadRequestError(fmt.Sprintf(
			"allowed_tools contains unknown tools: %s", strings.Join(check.Rejected, ", ")))
	}
	config.AllowedTools = check.Tools
	if len(check.KeptUnknown) == 0 && len(check.Dropped) == 0 {
		return nil, nil
	}
	return &allowedToolsWarnings{KeptUnknown: check.KeptUnknown, Dropped: check.Dropped}, nil
}
