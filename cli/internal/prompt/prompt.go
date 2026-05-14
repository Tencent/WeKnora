// Package prompt defines the interactive-input abstraction. Production
// uses charmbracelet/huh (TTYPrompter) for password input; non-TTY
// contexts get AgentPrompter, which rejects every interactive call with
// ErrAgentNoPrompt so commands can map it to a typed missing-flag error.
package prompt

import "errors"

// ErrAgentNoPrompt is returned by any Prompter call made when the CLI is in
// agent / non-interactive mode. Commands should map this to a "missing flag"
// user error before returning to cobra.
var ErrAgentNoPrompt = errors.New("prompt: interactive input not available in agent/non-interactive mode")

// Prompter is the small abstraction the auth login flow needs. Tests inject
// scripted responses; production wires charmbracelet/huh.
type Prompter interface {
	// Input collects free-form text input from the user.
	Input(label, defaultValue string) (string, error)
	// Password collects a secret without echoing.
	Password(label string) (string, error)
	// Confirm asks for yes/no confirmation, used by destructive commands.
	Confirm(label string, defaultValue bool) (bool, error)
}

// AgentPrompter rejects all interactive calls. Use it as the Factory.Prompter
// closure result whenever agent mode is detected.
type AgentPrompter struct{}

func (AgentPrompter) Input(string, string) (string, error) { return "", ErrAgentNoPrompt }
func (AgentPrompter) Password(string) (string, error)      { return "", ErrAgentNoPrompt }
func (AgentPrompter) Confirm(string, bool) (bool, error)   { return false, ErrAgentNoPrompt }
