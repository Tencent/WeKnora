package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const askUserDescription = `Ask the user a question and wait for the answer before continuing.

Use it only when you cannot proceed without information or a decision that only the user can provide, such as:
- choosing between options that lead to different work;
- details that are missing and cannot be found with other tools;
- confirming a consequential choice.

Do not use it for anything you can look up, infer reasonably, or that you could simply ask at the end of your answer.
Ask one focused question at a time.

Shapes of a question:
- free text: only "question";
- choices: "options": ["first choice", "second choice"] — a JSON array, never an array encoded inside a string
  (set "multi_select" for several, "allow_free_text" to also accept a typed answer);
- a short form: "fields" with types text, number, select or multiselect.

Options and field labels are shown to the user as written, and the chosen text comes back as the answer. Write
them in the user's language as a person would say them ("No authentication", "API key"), never as internal ids
("api_key"), and offer only choices the next step accepts. Leave out a field you can fill yourself.

The user may take a long time, decline, or not answer at all; then you are told so and must decide how to continue
without the answer.`

var askUserSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "The question shown to the user", "maxLength": %[1]d},
    "options": {
      "type": "array", "maxItems": %[2]d,
      "items": {"type": "string", "maxLength": %[3]d},
      "description": "Choices the user picks from, worded for the user"
    },
    "multi_select": {"type": "boolean", "description": "Allow choosing several options"},
    "allow_free_text": {"type": "boolean", "description": "Also accept a typed answer besides the options"},
    "fields": {
      "type": "array", "maxItems": %[4]d,
      "description": "A short form; use instead of options",
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "maxLength": %[5]d, "description": "Key of the value in the answer"},
          "label": {"type": "string", "maxLength": %[3]d},
          "type": {"type": "string", "enum": ["text", "number", "select", "multiselect"]},
          "required": {"type": "boolean"},
          "options": {"type": "array", "maxItems": %[2]d, "items": {"type": "string", "maxLength": %[3]d}}
        },
        "required": ["name", "type"]
      }
    }
  },
  "required": ["question"]
}`, types.AskUserMaxQuestionLen, types.AskUserMaxOptions, types.AskUserMaxOptionLen,
	types.AskUserMaxFields, types.AskUserMaxNameLen))

// AskUserTool asks the user a question through a card in the conversation.
type AskUserTool struct {
	BaseTool
	asker approval.UserAsker
}

// NewAskUserTool builds the ask_user tool.
func NewAskUserTool(asker approval.UserAsker) *AskUserTool {
	return &AskUserTool{BaseTool: NewBaseTool(ToolAskUser, askUserDescription, askUserSchema), asker: asker}
}

// Execute implements types.Tool.
func (t *AskUserTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var question types.AskUserPromptRequest
	if err := json.Unmarshal(args, &question); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to parse args: %v", err)}, nil
	}
	if err := question.Validate(); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Invalid question: %v", err)}, nil
	}
	meta, _ := ToolExecFromContext(ctx)
	resumed := meta != nil && meta.Answered != nil && meta.Answered.Kind == types.AgentPromptKindAskUser
	if !resumed && (!meta.Attended() || meta.EventBus == nil || t.asker == nil) {
		return &types.ToolResult{
			Success: true,
			Output: "This conversation cannot show an interactive question. Ask the user directly in your " +
				"reply, then end your turn; their next message will be the answer.",
		}, nil
	}

	var answer approval.Answer
	if resumed {
		answer = t.asker.ResumeAnswer(ctx, meta.Answered, meta.EventBus)
	} else {
		waitCtx := ctx
		if meta.ApprovalCtx != nil {
			waitCtx = meta.ApprovalCtx
		}
		var snapshot []byte
		var snapshotVersion int
		if meta.Snapshot != nil {
			var err error
			if snapshot, snapshotVersion, err = meta.Snapshot(); err != nil {
				logger.Warnf(ctx, "[Tool][AskUser] capture run state: %v", err)
				snapshot = nil
			}
		}
		tenantID := types.CallerFromContext(ctx).TenantID
		var err error
		answer, err = t.asker.AskUser(waitCtx, approval.AskRequest{
			TenantID:           tenantID,
			UserID:             meta.UserID,
			SessionID:          meta.SessionID,
			AssistantMessageID: meta.AssistantMessageID,
			RequestID:          meta.RequestID,
			ToolCallID:         meta.ToolCallID,
			EventBus:           meta.EventBus,
			Question:           question,
			Snapshot:           snapshot,
			SnapshotVersion:    snapshotVersion,
		})
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("Asking the user failed: %v", err)}, nil
		}
	}
	if answer.Suspended != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "waiting for the user to answer",
			Suspend: answer.Suspended,
		}, nil
	}
	data := map[string]interface{}{
		"display_type": "ask_user",
		"question":     question,
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
			Output: fmt.Sprintf("The user did not answer (%s). Continue without this information, "+
				"or tell the user what you need.", reason),
			Data: data,
		}, nil
	}
	data["answer"] = answer.Response
	return &types.ToolResult{Success: true, Output: formatAskUserAnswer(question, answer.Response), Data: data}, nil
}

func formatAskUserAnswer(q types.AskUserPromptRequest, a types.AskUserPromptResponse) string {
	var b strings.Builder
	b.WriteString("The user answered.\n")
	if len(a.Selected) > 0 {
		fmt.Fprintf(&b, "Selected: %s\n", strings.Join(a.Selected, ", "))
	}
	if len(a.Values) > 0 {
		b.WriteString("Fields:\n")
		order := make([]string, 0, len(a.Values))
		for _, f := range q.Fields {
			if _, ok := a.Values[f.Name]; ok {
				order = append(order, f.Name)
			}
		}
		for _, name := range order {
			v, _ := json.Marshal(a.Values[name])
			fmt.Fprintf(&b, "- %s: %s\n", name, v)
		}
	}
	if text := strings.TrimSpace(a.Text); text != "" {
		fmt.Fprintf(&b, "Text: %s\n", text)
	}
	return strings.TrimRight(b.String(), "\n")
}
