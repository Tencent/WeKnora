package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type browserTaskManager interface {
	GetStatus(context.Context, browserskill.Scope, string) (browserskill.Status, error)
	Account(context.Context, browserskill.Scope) (browserskill.AccountStatus, error)
	Control(context.Context, browserskill.Scope, string, string) error
	Call(context.Context, browserskill.Scope, string, string, map[string]any) (json.RawMessage, error)
	FinishTurn(context.Context, browserskill.Scope, string, bool) error
}

// BrowserSkillTool binds native browser commands to one member and conversation.
type BrowserSkillTool struct {
	BaseTool
	manager    browserTaskManager
	scope      browserskill.Scope
	session    string
	prepare    sync.Once
	prepareErr error
	used       atomic.Bool
	keepOpen   atomic.Bool
	failed     atomic.Bool
	blocked    atomic.Pointer[string]

	// One agent turn reuses a target-policy DOM context until the page or tab
	// changes. Strict sessions keep the 0.4.0 single-use contract.
	controlPolicy string
	contextID     string
	observationID string
	contextReady  bool
}

// BrowserSkillScope names the caller's browser. A shared agent runs in its
// owner's workspace, but the browser stays paired to the member who asked.
func BrowserSkillScope(ctx context.Context) browserskill.Scope {
	caller := types.CallerFromContext(ctx)
	return browserskill.Scope{Tenant: caller.TenantID, User: caller.UserID}
}

// NewBrowserSkillTool creates a session-bound adapter to upstream RPC.
func NewBrowserSkillTool(
	manager *browserskill.Manager,
	scope browserskill.Scope,
	session string,
	searchInstructions ...string,
) *BrowserSkillTool {
	return &BrowserSkillTool{
		BaseTool: NewBaseTool(
			"local_browser",
			browserDescription(searchInstructions),
			json.RawMessage(browserToolParameters),
		),
		manager: manager,
		scope:   scope,
		session: session,
	}
}

// Execute validates tool arguments and dispatches through the authorized task.
func (t *BrowserSkillTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if BrowserSkillScope(ctx) != t.scope {
		return nil, errors.New("local browser owner mismatch")
	}
	if reason := t.blocked.Load(); reason != nil {
		return &types.ToolResult{Success: false, Error: *reason, Output: *reason}, nil
	}
	args = normalizeBrowserArguments(args)
	if err := t.ValidateArguments(args); err != nil {
		t.failed.Store(true)
		return &types.ToolResult{Success: false, Error: "Invalid browser arguments: " + err.Error()}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	method := input["method"].(string)
	if keep, supplied := input["keep_open"].(bool); supplied {
		t.keepOpen.Store(keep)
	}
	params := browserCallParams(method, input)
	if method == "request_help" {
		t.keepOpen.Store(true)
	}
	status, err := t.manager.GetStatus(ctx, t.scope, t.session)
	if err != nil {
		t.failed.Store(true)
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	if !status.Connected {
		t.failed.Store(true)
		account, err := t.manager.Account(ctx, t.scope)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   "Browser connection status unavailable; retry after the server recovers.",
			}, nil
		}
		if account.Device != nil {
			return &types.ToolResult{
				Success: false,
				Error: "BrowserSkill is authorized but offline. Ask the user to keep Chrome and the extension open " +
					"while it reconnects automatically; do not ask to pair again. Any interrupted task must be " +
					"resumed from the conversation preview.",
			}, nil
		}
		return &types.ToolResult{
			Success: false,
			Error: "Connect BrowserSkill in personal settings > Browser connection, then resume any interrupted " +
				"task.",
		}, nil
	}
	t.controlPolicy = browserControlPolicy(status.ProtocolVersion)
	// Prepare once per agent turn. Ending a task cannot be undone by a later
	// call from the same turn; a new user turn can begin a new browser task.
	t.prepare.Do(func() { t.prepareErr = t.manager.Control(ctx, t.scope, t.session, "select") })
	if t.prepareErr != nil {
		t.failed.Store(true)
		return &types.ToolResult{Success: false, Error: t.prepareErr.Error()}, nil
	}
	t.used.Store(true)
	targeted := browserTargetedAction(method, params)
	needsCooperation := browserActionNeedsCooperation(method)
	// A first navigation bootstraps shared control only when this conversation
	// has no browser task yet. An existing task already has a fixed policy and
	// requires the context from cooperate sync before navigating.
	bootstrap := browserBootstrapAction(method) && !t.contextReady && status.SessionID == ""
	if needsCooperation && !bootstrap {
		if t.controlPolicy == "strict" || !targeted || !t.contextReady {
			if syncErr := t.ensureCooperation(ctx); syncErr != nil {
				t.failed.Store(true)
				return browserToolFailure(method, syncErr), nil
			}
		}
	}
	if needsCooperation && !bootstrap {
		if t.contextID != "" {
			params["context_id"] = t.contextID
		}
		if t.controlPolicy == "target" && !targeted && t.observationID != "" {
			params["observation_id"] = t.observationID
		}
	}
	if t.controlPolicy == "strict" {
		// expect is a target-policy assertion; a 0.4.0 extension rejects it.
		delete(params, "expect")
	}
	result, err := t.manager.Call(ctx, t.scope, t.session, method, params)
	if err != nil {
		t.invalidateContext()
		t.failed.Store(true)
		return browserToolFailure(method, err), nil
	}
	if browserActionInvalidatesContext(method) ||
		needsCooperation && (t.controlPolicy == "strict" || !targeted) {
		t.invalidateContext()
	}
	return t.interpretResult(method, result), nil
}

// ensureCooperation obtains the context this action needs. Target DOM actions
// reuse one context; scripts, untargeted actions and strict sessions sync per
// action. A 0.4.0 extension is selected as strict before the first sync.
func (t *BrowserSkillTool) ensureCooperation(ctx context.Context) error {
	policy := t.controlPolicy
	if policy == "" {
		policy = "target"
	}
	contextID, observationID, err := t.syncCooperation(ctx, policy)
	if err != nil && policy == "target" && controlPolicyUnsupported(err) {
		t.controlPolicy = "strict"
		contextID, observationID, err = t.syncCooperation(ctx, "strict")
	}
	if err != nil {
		return err
	}
	t.contextID = contextID
	t.observationID = observationID
	t.contextReady = true
	return nil
}

// syncCooperation obtains a shared-control context. A page can change while
// the extension captures its snapshot; wait for the shared tab to settle and
// retry once instead of surfacing a transient failure.
func (t *BrowserSkillTool) syncCooperation(ctx context.Context, policy string) (string, string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		result, err := t.manager.Call(ctx, t.scope, t.session, "cooperate", map[string]any{
			"action": "sync", "control_policy": policy,
		})
		if err == nil {
			var sync struct {
				ContextID     string `json:"context_id"`
				ObservationID string `json:"observation_id"`
				ControlPolicy string `json:"control_policy"`
			}
			if json.Unmarshal(result, &sync) == nil && sync.ContextID != "" {
				if sync.ControlPolicy != "" {
					t.controlPolicy = sync.ControlPolicy
				}
				return sync.ContextID, sync.ObservationID, nil
			}
			err = errors.New("BrowserSkill cooperate sync returned no context")
		}
		lastErr = err
		if attempt == 0 && cooperationSyncRetryable(err) {
			_, _ = t.manager.Call(ctx, t.scope, t.session, "cooperate",
				map[string]any{"action": "wait", "timeout_ms": 1500})
			continue
		}
		break
	}
	return "", "", lastErr
}

func controlPolicyUnsupported(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "control_policy") ||
		strings.Contains(message, "unknown field") && strings.Contains(message, "target") ||
		strings.Contains(message, "unsupported")
}

func (t *BrowserSkillTool) invalidateContext() {
	t.contextID = ""
	t.observationID = ""
	t.contextReady = false
}

func cooperationSyncRetryable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "page changed while synchronizing") ||
		strings.Contains(message, "cooperation_sync_required") ||
		strings.Contains(message, "sync again after it settles")
}

// browserControlPolicy enables target semantics on protocol 1.8+. The
// extension's reported version is informational: a 0.4.1 install can still
// report 0.4.0 while shipping the 1.8 protocol.
func browserControlPolicy(protocol string) string {
	if protocol == "" {
		return "target"
	}
	parts := strings.Split(strings.TrimSpace(protocol), ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "target"
	}
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if major > 1 || major == 1 && minor >= 8 {
		return "target"
	}
	return "strict"
}

// browserTargetedAction matches the extension's TARGET_ACTIONS rule: a DOM
// action with an explicit ref/selector, not a visual coordinate action.
func browserTargetedAction(method string, params map[string]any) bool {
	switch method {
	case "click", "fill", "press", "select", "hover", "focus", "blur", "scroll_to", "wheel":
	default:
		return false
	}
	_, ref := params["ref"]
	_, selector := params["selector"]
	return ref || selector
}

func browserActionInvalidatesContext(method string) bool {
	switch method {
	case "navigate", "navigate_back", "navigate_forward", "reload",
		"tab_create", "tab_select", "tab_close", "tab_borrow", "tab_return",
		"emulate", "window_resize", "evaluate", "wait_ms", "wait_for_navigation",
		"request_help":
		return true
	default:
		return false
	}
}

// browserActionNeedsCooperation reports whether the extension now requires a
// fresh shared-control context for this action. Passive reads and session/tab
// management stay outside the single-use context.
func browserActionNeedsCooperation(method string) bool {
	switch method {
	case "observe", "snapshot", "get_html", "screenshot", "console", "network",
		"tab_list", "wait_for_navigation", "request_help",
		"tab_create", "tab_borrow", "tab_select", "tab_return":
		return false
	default:
		return true
	}
}

// browserBootstrapAction lists the actions the extension permits before the
// first cooperate sync. They still use a context when one is available.
func browserBootstrapAction(method string) bool {
	switch method {
	case "navigate", "emulate", "window_resize", "tab_close":
		return true
	default:
		return false
	}
}

// Cleanup reclaims successful temporary tasks; unfinished work retains its pages.
// Execute can exit with a cancelled context; cleanup must still reach the extension.
func (t *BrowserSkillTool) Cleanup(ctx context.Context) {
	if !t.used.Swap(false) {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := t.manager.FinishTurn(cleanupCtx, t.scope, t.session,
		t.keepOpen.Load() || t.failed.Load() || ctx.Err() != nil); err != nil {
		logger.Warnf(cleanupCtx, "Failed to clean up local browser task: %v", err)
	}
}
