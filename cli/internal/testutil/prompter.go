package testutil

import (
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// ConfirmPrompter is a test double for prompt.Prompter that scripts a single
// Confirm answer (with optional error). Input/Password are stubbed to return
// prompt.ErrAgentNoPrompt - assert it via `Asked` after the call.
//
// Use across cmd/* tests where a command's confirm-prompt branch needs to be
// exercised. Avoid maintaining per-command copies of the same shape.
type ConfirmPrompter struct {
	Answer bool
	Err    error
	Asked  bool
}

func (c *ConfirmPrompter) Input(string, string) (string, error) {
	return "", prompt.ErrAgentNoPrompt
}

func (c *ConfirmPrompter) Password(string) (string, error) {
	return "", prompt.ErrAgentNoPrompt
}

func (c *ConfirmPrompter) Confirm(string, bool) (bool, error) {
	c.Asked = true
	return c.Answer, c.Err
}
