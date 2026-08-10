package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestToolExecutionTimeout(t *testing.T) {
	assert.Equal(t, 10*time.Minute+5*time.Second, toolExecutionTimeout("shell_exec"))
	assert.Equal(t, 60*time.Second, toolExecutionTimeout("web_fetch"))
}
