package prompt

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentPrompter_AlwaysRejects(t *testing.T) {
	p := AgentPrompter{}
	_, err := p.Input("label", "default")
	assert.True(t, errors.Is(err, ErrAgentNoPrompt))
	_, err = p.Password("password")
	assert.True(t, errors.Is(err, ErrAgentNoPrompt))
	_, err = p.Confirm("yes?", false)
	assert.True(t, errors.Is(err, ErrAgentNoPrompt))
}
