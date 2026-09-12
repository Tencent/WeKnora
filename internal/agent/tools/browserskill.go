package tools

import (
	"context"
	"encoding/json"
	"errors"
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
	tenant, _ := types.TenantIDFromContext(ctx)
	user, _ := types.UserIDFromContext(ctx)
	if tenant != t.scope.Tenant || user != t.scope.User {
		return nil, errors.New("local browser owner mismatch")
	}
	if reason := t.blocked.Load(); reason != nil {
		return &types.ToolResult{Success: false, Error: *reason, Output: *reason}, nil
	}
	if err := t.ValidateArguments(args); err != nil {
		t.failed.Store(true)
		return &types.ToolResult{Success: false, Error: "Invalid browser arguments: " + err.Error()}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	method := input["method"].(string)
	delete(input, "method")
	if keep, supplied := input["keep_open"].(bool); supplied {
		t.keepOpen.Store(keep)
	}
	delete(input, "keep_open")
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
	// Prepare once per agent turn. Ending a task cannot be undone by a later
	// call from the same turn; a new user turn can begin a new browser task.
	t.prepare.Do(func() { t.prepareErr = t.manager.Control(ctx, t.scope, t.session, "select") })
	if t.prepareErr != nil {
		t.failed.Store(true)
		return &types.ToolResult{Success: false, Error: t.prepareErr.Error()}, nil
	}
	t.used.Store(true)
	result, err := t.manager.Call(ctx, t.scope, t.session, method, input)
	if err != nil {
		t.failed.Store(true)
		return browserToolFailure(method, err), nil
	}
	return t.interpretResult(method, result), nil
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
