package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestToolExecutionTimeout(t *testing.T) {
	assert.Equal(t, 10*time.Minute+5*time.Second, toolExecutionTimeout("shell_exec"))
	assert.Equal(t, 60*time.Second, toolExecutionTimeout("web_fetch"))
	for _, method := range []string{"request_help", "tab_borrow"} {
		assert.Equal(t, 5*time.Minute+15*time.Second,
			toolExecutionTimeout("local_browser", `{"method":"`+method+`"}`))
	}
	for _, args := range []string{`{"method":"click"}`, `{"method":"observe"}`, `{`} {
		assert.Equal(t, 60*time.Second, toolExecutionTimeout("local_browser", args))
	}
}
