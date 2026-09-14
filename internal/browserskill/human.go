package browserskill

import (
	"encoding/json"
	"time"
)

const (
	// HumanStepWait matches BrowserSkill's default help window.
	HumanStepWait = 5 * time.Minute
	// HumanStepTimeout leaves time for the native result before cancellation.
	HumanStepTimeout = HumanStepWait + 15*time.Second
)

// IsHumanStep identifies browser operations that can wait for user input.
func IsHumanStep(method string) bool {
	return method == "request_help" || method == "tab_borrow"
}

func clusterTimeout(operation, method string) time.Duration {
	if operation == "call" && IsHumanStep(method) {
		return HumanStepTimeout
	}
	return 2 * time.Minute
}

func humanTimeoutMS(value any) int64 {
	// Local callers use Go integers; cluster and model inputs use JSON numbers.
	raw, _ := json.Marshal(value)
	var ms float64
	if json.Unmarshal(raw, &ms) == nil && ms >= 1 && ms <= float64(HumanStepWait.Milliseconds()) {
		return int64(ms)
	}
	return HumanStepWait.Milliseconds()
}

func pausedError() *RPCError {
	return &RPCError{
		Code: "task_paused",
		Message: "Browser is connected, but this task is paused for user intervention. " +
			"Ask the user to complete any manual step, click Continue operation in the conversation browser preview, " +
			"then continue in this same conversation. Do not reconnect or pair again. Observe before acting.",
	}
}
