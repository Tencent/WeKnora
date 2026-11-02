// internal/agent/tools/secure_form.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

const requestSecureFormDescription = `Ask the user for login credentials or one-time codes through a secure card, for the page open in the sandbox browser.

Use it whenever the sandbox browser reaches a login, password, verification-code or similar secret prompt. Never ask for passwords or codes in the chat, and never type them yourself.

The values never reach you: you get a handle such as cred_1. Take a fresh "agent-browser snapshot -i", then call browser_fill_secret with the handle and the refs of the inputs. Submitting the form (clicking "Sign in") is still your job.

- site: the host of the page now open in the browser, e.g. accounts.google.com.
- domains: other hosts of the same site the login needs (at most 5), e.g. login.example.com for example.com; they must share the site's registrable domain. Values are only ever filled into these.
- kind "login" with parts ["username","password"] (or one of them) for an account login; extra for other inputs such as a one-time code.
- allow_save: false when the user should not be offered to save the login.

The user may decline or not answer; then you are told so and must continue without the credentials.`

var requestSecureFormSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "maxLength": %[1]d, "description": "Card title in the user's language, e.g. \"Sign in to GitHub\""},
    "kind": {"type": "string", "enum": ["login"], "description": "\"login\" for an account login; omit for extra fields only"},
    "site": {"type": "string", "description": "Host of the page now open in the sandbox browser"},
    "domains": {"type": "array", "maxItems": %[2]d, "items": {"type": "string"}, "description": "Other hosts under the site's registrable domain the values may be filled into"},
    "parts": {"type": "array", "items": {"type": "string", "enum": ["username", "password"]}, "description": "Login parts to ask for; default both"},
    "prefill": {"type": "object", "properties": {"username": {"type": "string"}}, "description": "Non-secret values shown pre-filled"},
    "extra": {
      "type": "array", "maxItems": %[3]d,
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "description": "lowercase key, e.g. otp"},
          "label": {"type": "string", "maxLength": %[4]d},
          "type": {"type": "string", "enum": ["text", "email", "password", "one_time_code", "number"]},
          "required": {"type": "boolean"}
        },
        "required": ["name", "type"]
      }
    },
    "allow_save": {"type": "boolean", "description": "Offer saving the login to the user's vault; default true"}
  },
  "required": ["title", "site"]
}`, types.SecureFormMaxTitleLen, types.SecureFormMaxDomains, types.SecureFormMaxExtra, types.SecureFormMaxLabelLen))

// RequestSecureFormTool shows a secure credential card and returns a handle.
type RequestSecureFormTool struct {
	BaseTool
	asker   approval.SecureFormAsker
	browser sandbox.SessionSecureBrowser
}

// NewRequestSecureFormTool builds the request_secure_form tool.
func NewRequestSecureFormTool(asker approval.SecureFormAsker, browser sandbox.SessionSecureBrowser) *RequestSecureFormTool {
	return &RequestSecureFormTool{
		BaseTool: NewBaseTool(ToolRequestSecureForm, requestSecureFormDescription, requestSecureFormSchema),
		asker:    asker,
		browser:  browser,
	}
}

// Execute implements types.Tool.
func (t *RequestSecureFormTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var form types.SecureFormPromptRequest
	if err := json.Unmarshal(args, &form); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to parse args: %v", err)}, nil
	}
	form.ObservedOrigin = ""
	if err := form.Validate(); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Invalid form: %v", err)}, nil
	}
	meta, _ := ToolExecFromContext(ctx)
	resumed := meta != nil && meta.Answered != nil && meta.Answered.Kind == types.AgentPromptKindSecureForm
	if !resumed && (!meta.Attended() || meta.EventBus == nil || t.asker == nil) {
		return &types.ToolResult{
			Success: true,
			Output: "This conversation cannot show a secure credential form. Ask the user to open the Desktop " +
				"tab and type the credentials into the browser themselves, then end your turn. " +
				"Never ask for passwords or codes in the chat.",
		}, nil
	}

	var answer approval.SecureFormAnswer
	if resumed {
		answer = t.asker.ResumeSecureForm(ctx, meta.Answered, meta.EventBus)
	} else {
		form.ObservedOrigin = t.observedOrigin(ctx, meta.SessionID)
		waitCtx := ctx
		if meta.ApprovalCtx != nil {
			waitCtx = meta.ApprovalCtx
		}
		var snapshot []byte
		var snapshotVersion int
		if meta.Snapshot != nil {
			var err error
			if snapshot, snapshotVersion, err = meta.Snapshot(); err != nil {
				logger.Warnf(ctx, "[Tool][SecureForm] capture run state: %v", err)
				snapshot = nil
			}
		}
		var err error
		answer, err = t.asker.RequestSecureForm(waitCtx, approval.SecureFormRequest{
			TenantID:           types.CallerFromContext(ctx).TenantID,
			UserID:             meta.UserID,
			SessionID:          meta.SessionID,
			AssistantMessageID: meta.AssistantMessageID,
			RequestID:          meta.RequestID,
			ToolCallID:         meta.ToolCallID,
			EventBus:           meta.EventBus,
			Form:               form,
			Snapshot:           snapshot,
			SnapshotVersion:    snapshotVersion,
		})
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("Showing the credential form failed: %v", err)}, nil
		}
	}
	if answer.Suspended != nil {
		return &types.ToolResult{Success: false, Error: "waiting for the user to submit credentials", Suspend: answer.Suspended}, nil
	}

	data := map[string]interface{}{
		"display_type": "secure_form",
		"request":      form,
		"answered":     answer.Answered,
	}
	if !answer.Answered {
		reason := answer.Reason
		if reason == "" {
			reason = "no answer"
		}
		data["reason"] = reason
		data["timed_out"] = answer.TimedOut
		data["canceled"] = answer.Canceled
		return &types.ToolResult{
			Success: true,
			Output: fmt.Sprintf("The user did not provide credentials (%s). Do not ask for them in the chat; "+
				"continue without them or let the user sign in on the Desktop tab.", reason),
			Data: data,
		}, nil
	}
	r := answer.Response
	data["handle"] = r.Handle
	data["fields"] = r.Fields
	data["username"] = r.Username
	data["saved"] = r.Saved
	return &types.ToolResult{Success: true, Output: formatSecureFormAnswer(form, r), Data: data}, nil
}

func (t *RequestSecureFormTool) observedOrigin(ctx context.Context, sessionID string) string {
	if t.browser == nil {
		return ""
	}
	page, err := t.browser.CurrentPage(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[Tool][SecureForm] read browser page: %v", err)
		return ""
	}
	return pageOrigin(page.URL)
}

func pageOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func formatSecureFormAnswer(form types.SecureFormPromptRequest, r types.SecureFormPromptResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The user provided credentials for %s.\n", form.Site)
	fmt.Fprintf(&b, "handle: %s  fields: %s", r.Handle, strings.Join(r.Fields, ", "))
	if r.Username != "" {
		fmt.Fprintf(&b, "  username: %s", r.Username)
	}
	fmt.Fprintf(&b, "  saved: %t\n", r.Saved)
	b.WriteString("Take a fresh snapshot, then call browser_fill_secret with this handle.")
	return b.String()
}
