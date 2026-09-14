package tools

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
)

func browserToolFailure(method string, err error) *types.ToolResult {
	var rpcErr *browserskill.RPCError
	if !errors.As(err, &rpcErr) {
		return &types.ToolResult{Success: false, Error: err.Error(), Output: err.Error()}
	}
	output, _ := json.Marshal(struct {
		Method string                 `json:"method"`
		Error  *browserskill.RPCError `json:"error"`
		Hint   string                 `json:"recovery_hint"`
	}{method, rpcErr, browserRecoveryHint(method, rpcErr)})
	return &types.ToolResult{Success: false, Error: err.Error(), Output: string(output)}
}

func browserRecoveryHint(method string, err *browserskill.RPCError) string {
	var data struct {
		Reason string `json:"reason"`
		Effect string `json:"effect_state"`
	}
	_ = json.Unmarshal(err.Data, &data)
	if err.Code == "user_aborted" || err.Code == "cancelled" || err.Code == "task_paused" || err.Code == "timeout" {
		return "Stop browser actions. Ask the user to complete any manual step and click Continue operation " +
			"in the conversation browser preview, then continue in the same conversation. " +
			"A paused task does not require reconnection or pairing. The action may already have taken effect. " +
			"Observe before acting; do not replay the interrupted action."
	}
	if data.Effect == "unknown" || data.Effect == "committed" {
		return "The action may already have taken effect. Observe current state before deciding " +
			"what remains; do not repeat the action blindly."
	}
	switch data.Reason {
	case "ref_not_found":
		return "Observe the current tab and retry the intended action once using its fresh ref. " +
			"Do not guess another ref."
	case "element_not_visible":
		return "Observe the active dialog/menu and target visibility. Reveal the intended control " +
			"before acting; do not click adjacent refs or hidden elements."
	case "selector_not_found":
		return "Observe the current page and locate the intended target. Do not guess selectors; " +
			"refs can address iframe/shadow-root controls."
	case "target_not_fillable", "fill_value_invalid", "fill_target_changed", "fill_focus_lost",
		"fill_value_mismatch", "fill_failed":
		return "Observe the current editable field and its value first. The page may have changed " +
			"or formatted it. Correct only the remaining difference; do not blindly repeat fill."
	case "target_not_select", "option_not_found", "single_select_value_count":
		return "select requires a native select and its option values. Inspect current options; " +
			"use click/observe for a custom dropdown."
	case "agent_window_scope", "borrow_conflict":
		return "List actual user tabs and respect tab ownership. Borrow the intended available tab " +
			"through extension confirmation; never guess its ID."
	}
	switch err.Code {
	case "invalid_params":
		if method == "press" {
			return "press accepts a key or shortcut such as Enter or Ctrl+A. For text input use " +
				"fill with a fresh ref and value."
		}
		return "Use the method's documented fields at the top level. Correct the reported argument " +
			"instead of guessing alternative parameter names."
	case "timeout", "cdp_failed":
		return "Inspect current page state before retrying: the action may already have occurred. " +
			"Stop repeated attempts without new evidence; do not automatically reload or replay " +
			"mutations."
	case "not_found":
		return "Refresh the current tab/page state and use returned identifiers. Do not invent " +
			"tab, session or element IDs."
	default:
		return "Use the error details to identify the blocker. Do not repeat an unchanged failed " +
			"action or bypass browser permissions."
	}
}

// RPC success and page-operation success are different contracts. Inspect only
// known result envelopes; arbitrary page text containing 'error' is still data.
func (t *BrowserSkillTool) interpretResult(method string, raw json.RawMessage) *types.ToolResult {
	if method == "screenshot" {
		result := browserScreenshotResult(raw)
		if !result.Success {
			t.failed.Store(true)
		}
		return result
	}
	result := &types.ToolResult{Success: true, Output: string(raw)}
	if browserskill.NavigationIncomplete(method, raw) {
		result.Success = false
		result.Error = "Navigation did not reach the requested loading phase. The page may already have changed. " +
			"Observe current state before deciding what remains; do not automatically reload or repeat navigation."
		t.failed.Store(true)
		return result
	}
	if method != "evaluate" && method != "request_help" {
		return result
	}
	var envelope struct {
		OK      *bool  `json:"ok"`
		Outcome string `json:"outcome"`
	}
	err := json.Unmarshal(raw, &envelope)
	if method == "evaluate" {
		if err == nil && envelope.OK != nil && *envelope.OK {
			return result
		}
		result.Error = "Browser evaluate failed or returned an invalid result. Inspect ok/error; " +
			"return bounded serializable data, not DOM nodes. Observe before retrying."
	} else {
		if err == nil && (envelope.Outcome == "continued" || envelope.Outcome == "completed") {
			return result
		}
		result.Error = fmt.Sprintf(
			"Browser human step did not complete (outcome=%q). "+
				"Stop browser actions until the user resumes in a new turn.",
			envelope.Outcome,
		)
		reason := result.Error
		t.blocked.Store(&reason)
	}
	result.Success = false
	t.failed.Store(true)
	return result
}
