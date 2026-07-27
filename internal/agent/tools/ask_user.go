package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var askUserBaseTool = BaseTool{
	name: ToolAskUser,
	description: `Ask the current Web user one structured question and wait for their answer before continuing.

Use this tool only when missing information materially blocks a reliable answer or action. Do not ask for facts already present in the conversation. For a related sequence, plan the total first, keep the same question_group_id, ask one question per call, and stop early when later questions become unnecessary. Never ask for passwords, access tokens, private keys, or equivalent secrets. A skipped answer is the user's choice and must not be repeatedly forced.`,
	schema: utils.GenerateSchema[AskUserInput](),
}

// AskUserInput is the model-facing tool contract. Pointer fields distinguish
// omitted defaults from explicit values.
type AskUserInput struct {
	Question        string             `json:"question" jsonschema:"The one question to show, maximum 500 characters"`
	Mode            userinput.Mode     `json:"mode" jsonschema:"Selection mode: single_choice or multiple_choice"`
	QuestionGroupID string             `json:"question_group_id" jsonschema:"Stable ASCII identifier shared by related questions"`
	QuestionIndex   *int               `json:"question_index,omitempty" jsonschema:"Optional one-based position in the planned question group; defaults to 1"`
	QuestionTotal   *int               `json:"question_total,omitempty" jsonschema:"Optional planned group size; defaults to 1"`
	Options         []userinput.Option `json:"options" jsonschema:"Two to eight predefined options"`
	AllowOther      *bool              `json:"allow_other,omitempty" jsonschema:"Whether free-form Other input is allowed; defaults to true"`
	AllowSkip       *bool              `json:"allow_skip,omitempty" jsonschema:"Whether the user may skip; defaults to true"`
}

// AskUserTool pauses a live Agent run through the user-input gate.
type AskUserTool struct {
	BaseTool
	requester userinput.Requester
}

func NewAskUserTool(requester userinput.Requester) *AskUserTool {
	return &AskUserTool{BaseTool: askUserBaseTool, requester: requester}
}

func (t *AskUserTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input AskUserInput
	if err := json.Unmarshal(args, &input); err != nil {
		return failedAskUserResult(fmt.Sprintf("Failed to parse args: %v", err)), nil
	}
	if t.requester == nil {
		return failedAskUserResult("structured user input is unavailable"), nil
	}
	meta, ok := ToolExecFromContext(ctx)
	if !ok || meta.EventBus == nil || meta.SessionID == "" || meta.UserID == "" {
		return failedAskUserResult("ask_user requires a live authenticated Agent execution"), nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return failedAskUserResult("ask_user requires a tenant context"), nil
	}

	question := userinput.Question{
		Text: input.Question, Mode: input.Mode, GroupID: input.QuestionGroupID,
		Index: intDefaultOne(input.QuestionIndex), Total: intDefaultOne(input.QuestionTotal), Options: input.Options,
		AllowOther: boolDefaultTrue(input.AllowOther), AllowSkip: boolDefaultTrue(input.AllowSkip),
	}
	if err := userinput.ValidateQuestion(question); err != nil {
		return failedAskUserResult(err.Error()), nil
	}
	waitCtx := ctx
	if meta.ApprovalCtx != nil {
		waitCtx = meta.ApprovalCtx
	}
	requestID := meta.RequestID
	if requestID == "" {
		requestID, _ = types.RequestIDFromContext(ctx)
	}
	result, err := t.requester.RequestAndWait(waitCtx, userinput.PendingRequest{
		TenantID: tenantID, UserID: meta.UserID, SessionID: meta.SessionID,
		AssistantMessageID: meta.AssistantMessageID, RequestID: requestID,
		ToolCallID: meta.ToolCallID, EventBus: meta.EventBus, Question: question,
	})
	if err != nil {
		return failedAskUserResult(err.Error()), err
	}
	output, err := json.Marshal(result)
	if err != nil {
		return failedAskUserResult("failed to encode structured answer"), err
	}
	data := make(map[string]interface{})
	_ = json.Unmarshal(output, &data)
	return &types.ToolResult{Success: true, Output: string(output), Data: data}, nil
}

func boolDefaultTrue(value *bool) bool {
	return value == nil || *value
}

func intDefaultOne(value *int) int {
	if value == nil {
		return 1
	}
	return *value
}

func failedAskUserResult(message string) *types.ToolResult {
	return &types.ToolResult{Success: false, Error: message}
}
