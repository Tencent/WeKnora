package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SecretHandles resolves credential handles for the caller that created them.
type SecretHandles interface {
	Resolve(ctx context.Context, tenantID uint64, userID, sessionID, handle string) (*types.ResolvedSecret, error)
	Complete(ctx context.Context, tenantID uint64, userID, sessionID, handle, vaultItemID string) error
}

const browserFillSecretDescription = `Fill the values behind a credential handle from
request_secure_form into inputs of the page open in the sandbox browser.

targets maps each field name of the handle to the ref of its input from your
latest snapshot, e.g. {"username":"@e2","password":"@e3"}. Values are only filled
on the domains the user approved, and password values only into password inputs.
The page is not submitted.

Each field reports filled, or why not:
- ref_not_found / not_editable: take a fresh snapshot and retry with the right ref;
- host_mismatch / insecure_origin: the browser is not on an approved https page;
- not_password_field: the ref is not a password input;
- page_changed: the page loaded another document while filling; take a fresh snapshot and retry;
- verify_failed: the input did not keep the value;
- handle_expired / credential_unavailable: call request_secure_form again.
Do not take screenshots or reveal the password field to check the result.`

var browserFillSecretSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "handle": {"type": "string", "description": "Handle from request_secure_form, e.g. cred_1"},
    "targets": {
      "type": "object",
      "additionalProperties": {"type": "string"},
      "description": "Field name -> snapshot ref, e.g. {\"username\":\"@e2\",\"password\":\"@e3\"}"
    }
  },
  "required": ["handle", "targets"]
}`)

var secretRefPattern = regexp.MustCompile(`^@?e[0-9]{1,6}$`)

// BrowserFillSecretTool fills a handle's values into the sandbox browser.
type BrowserFillSecretTool struct {
	BaseTool
	handles SecretHandles
	browser sandbox.SessionSecureBrowser
}

// NewBrowserFillSecretTool builds the browser_fill_secret tool.
func NewBrowserFillSecretTool(handles SecretHandles, browser sandbox.SessionSecureBrowser) *BrowserFillSecretTool {
	return &BrowserFillSecretTool{
		BaseTool: NewBaseTool(ToolBrowserFillSecret, browserFillSecretDescription, browserFillSecretSchema),
		handles:  handles,
		browser:  browser,
	}
}

type fillSecretArgs struct {
	Handle  string            `json:"handle"`
	Targets map[string]string `json:"targets"`
}

// Execute implements types.Tool.
func (t *BrowserFillSecretTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var in fillSecretArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to parse args: %v", err)}, nil
	}
	in.Handle = strings.TrimSpace(in.Handle)
	if !types.ValidSecretHandle(in.Handle) {
		return &types.ToolResult{Success: false, Error: "handle must look like cred_1"}, nil
	}
	if len(in.Targets) == 0 {
		return &types.ToolResult{Success: false, Error: "targets is required"}, nil
	}
	targets := make(map[string]string, len(in.Targets))
	for name, ref := range in.Targets {
		ref = strings.TrimSpace(ref)
		if !secretRefPattern.MatchString(ref) {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("target %q: %q is not a snapshot ref such as @e3", name, ref),
			}, nil
		}
		targets[name] = "@" + strings.TrimPrefix(ref, "@")
	}
	meta, ok := ToolExecFromContext(ctx)
	if !ok || meta.SessionID == "" || meta.UserID == "" {
		return &types.ToolResult{Success: false, Error: "browser_fill_secret needs a conversation session"}, nil
	}
	tenantID := types.CallerFromContext(ctx).TenantID

	secret, err := t.handles.Resolve(ctx, tenantID, meta.UserID, meta.SessionID, in.Handle)
	switch {
	case errors.Is(err, types.ErrSecretHandleNotFound):
		expired := uniformStatus(targets, types.SecretFillHandleExpired)
		return fillOutcome(in.Handle, sortedKeys(targets), expired), nil
	case errors.Is(err, types.ErrCredentialUnavailable):
		gone := uniformStatus(targets, types.SecretFillCredentialUnavailable)
		return fillOutcome(in.Handle, sortedKeys(targets), gone), nil
	case err != nil:
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("Reading the credential handle failed: %v", err),
		}, nil
	}

	known := make(map[string]bool, len(secret.Fields))
	names := make([]string, 0, len(secret.Fields))
	for _, f := range secret.Fields {
		known[f.Name] = true
		names = append(names, f.Name)
	}
	var unknown []string
	for name := range targets {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("unknown field(s) %s; %s carries: %s",
			strings.Join(unknown, ", "), in.Handle, strings.Join(names, ", "))}, nil
	}

	req := sandbox.SecretFillRequest{Domains: secret.Domains}
	var order []string
	for _, f := range secret.Fields {
		if ref, ok := targets[f.Name]; ok {
			req.Fields = append(req.Fields, sandbox.SecretFillField{
				Name: f.Name, Ref: ref, Type: f.Type, Value: f.Value,
			})
			order = append(order, f.Name)
		}
	}
	res, err := t.browser.FillSecret(ctx, meta.SessionID, req)
	for i := range req.Fields {
		req.Fields[i].Value = ""
	}
	if errors.Is(err, sandbox.ErrSecretFillHelperMissing) {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Filling the browser failed: %v", err)}, nil
	}
	statuses := make(map[string]string, len(order))
	for _, name := range order {
		s := res.Fields[name]
		if s == "" {
			s = types.SecretFillVerifyFailed
		}
		statuses[name] = s
	}
	if allFilled(names, statuses) {
		err := t.handles.Complete(
			ctx, tenantID, meta.UserID, meta.SessionID, in.Handle, secret.VaultItemID)
		if err != nil {
			logger.Warnf(ctx, "[Tool][FillSecret] complete handle %s: %v", in.Handle, err)
		}
	}
	return fillOutcome(in.Handle, order, statuses), nil
}

func allFilled(names []string, statuses map[string]string) bool {
	for _, n := range names {
		if statuses[n] != types.SecretFillFilled {
			return false
		}
	}
	return true
}

func uniformStatus(targets map[string]string, status string) map[string]string {
	out := make(map[string]string, len(targets))
	for name := range targets {
		out[name] = status
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var fillHints = map[string]string{
	types.SecretFillRefNotFound: "Take a fresh snapshot and retry with the current refs.",
	types.SecretFillNotEditable: "Take a fresh snapshot and retry with the ref of the input itself.",
	types.SecretFillHostMismatch: "The browser is not on a domain the user approved; " +
		"nothing more was filled.",
	types.SecretFillInsecureOrigin: "The page is not https; values are not filled there.",
	types.SecretFillNotPasswordField: "Password values only go into password inputs; " +
		"pick the password input's ref.",
	types.SecretFillPageChanged: "The page loaded another document while filling; " +
		"nothing more was filled. Take a fresh snapshot and retry.",
	types.SecretFillVerifyFailed: "The input did not keep the value; take a fresh snapshot and retry.",
	types.SecretFillHandleExpired: "The handle expired or was used up; " +
		"call request_secure_form again.",
	types.SecretFillCredentialUnavailable: "The saved credential is gone or unreadable; " +
		"call request_secure_form again.",
}

func fillOutcome(handle string, order []string, statuses map[string]string) *types.ToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "Fill results for %s:\n", handle)
	seen := map[string]bool{}
	var hints []string
	for _, name := range order {
		s := statuses[name]
		fmt.Fprintf(&b, "- %s: %s\n", name, s)
		if h, ok := fillHints[s]; ok && !seen[s] {
			seen[s] = true
			hints = append(hints, h)
		}
	}
	for _, h := range hints {
		b.WriteString(h + "\n")
	}
	return &types.ToolResult{
		Success: true,
		Output:  strings.TrimRight(b.String(), "\n"),
		Data: map[string]interface{}{
			"display_type": "browser_fill_secret",
			"handle":       handle,
			"fields":       statuses,
		},
	}
}
