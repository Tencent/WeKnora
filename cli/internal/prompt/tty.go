package prompt

import (
	"fmt"

	"github.com/charmbracelet/huh"
)

// TTYPrompter implements Prompter using charmbracelet/huh for interactive
// input. Used by the production Factory.Prompter closure when iostreams.IO
// is interactive.
type TTYPrompter struct{}

// NewTTYPrompter returns a Prompter backed by huh.
func NewTTYPrompter() Prompter { return TTYPrompter{} }

func (TTYPrompter) Input(label, defaultValue string) (string, error) {
	v := defaultValue
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(label).Value(&v),
	))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("prompt input: %w", err)
	}
	return v, nil
}

func (TTYPrompter) Password(label string) (string, error) {
	var v string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(label).EchoMode(huh.EchoModePassword).Value(&v),
	))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("prompt password: %w", err)
	}
	return v, nil
}

func (TTYPrompter) Confirm(label string, defaultValue bool) (bool, error) {
	v := defaultValue
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(label).Value(&v),
	))
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("prompt confirm: %w", err)
	}
	return v, nil
}
