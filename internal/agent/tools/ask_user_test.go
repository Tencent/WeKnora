package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeUserInputRequester struct {
	result userinput.Result
	err    error
	req    userinput.PendingRequest
	ctx    context.Context
	calls  int
}

func (f *fakeUserInputRequester) RequestAndWait(ctx context.Context, req userinput.PendingRequest) (userinput.Result, error) {
	f.ctx = ctx
	f.req = req
	f.calls++
	return f.result, f.err
}

func askUserArgs() json.RawMessage {
	return json.RawMessage(`{
        "question":"公司如何通知你解除劳动合同？",
        "mode":"single_choice",
        "question_group_id":"dismissal-facts",
        "question_index":1,
        "question_total":3,
        "options":[
            {"id":"written","label":"书面通知"},
            {"id":"verbal","label":"口头通知"}
        ]
    }`)
}

func askUserContext() (context.Context, context.Context) {
	base := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	approvalCtx := context.WithValue(base, struct{ name string }{"approval"}, true)
	ctx := WithToolExecContext(base, &ToolExecContext{
		SessionID: "session-1", AssistantMessageID: "message-1", RequestID: "request-1",
		ToolCallID: "tool-1", UserID: "user-1", EventBus: event.NewEventBus(),
		ApprovalCtx: approvalCtx,
	})
	return ctx, approvalCtx
}

func TestAskUserReturnsStructuredAnswer(t *testing.T) {
	requester := &fakeUserInputRequester{result: userinput.Result{
		Status: userinput.StatusAnswered, QuestionGroupID: "dismissal-facts",
		QuestionIndex: 1, QuestionTotal: 3,
		SelectedOptions: []userinput.Option{{ID: "written", Label: "书面通知"}},
	}}
	tool := NewAskUserTool(requester)
	ctx, approvalCtx := askUserContext()
	result, err := tool.Execute(ctx, askUserArgs())
	if err != nil || !result.Success {
		t.Fatalf("Execute() result = %+v, error = %v", result, err)
	}
	if requester.calls != 1 || requester.ctx != approvalCtx {
		t.Fatalf("requester calls = %d, used approval context = %v", requester.calls, requester.ctx == approvalCtx)
	}
	if requester.req.TenantID != 10000 || requester.req.UserID != "user-1" || requester.req.SessionID != "session-1" {
		t.Fatalf("pending request = %+v", requester.req)
	}
	if !requester.req.Question.AllowOther || !requester.req.Question.AllowSkip {
		t.Fatalf("default flags = other:%v skip:%v", requester.req.Question.AllowOther, requester.req.Question.AllowSkip)
	}
	var output userinput.Result
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil || output.Status != userinput.StatusAnswered {
		t.Fatalf("output = %q, error = %v", result.Output, err)
	}
}

func TestAskUserPreservesExplicitFalseFlags(t *testing.T) {
	requester := &fakeUserInputRequester{result: userinput.Result{Status: userinput.StatusSkipped}}
	tool := NewAskUserTool(requester)
	ctx, _ := askUserContext()
	args := json.RawMessage(`{
        "question":"请选择通知方式","mode":"single_choice","question_group_id":"dismissal",
        "question_index":1,"question_total":1,"allow_other":false,"allow_skip":false,
        "options":[{"id":"written","label":"书面"},{"id":"verbal","label":"口头"}]
    }`)
	_, _ = tool.Execute(ctx, args)
	if requester.req.Question.AllowOther || requester.req.Question.AllowSkip {
		t.Fatalf("explicit false flags were not preserved: %+v", requester.req.Question)
	}
}

func TestAskUserSchemaCastsStringifiedProviderArgs(t *testing.T) {
	args := json.RawMessage(`{
		"question":"你更偏好哪种工作方式？","mode":"single_choice",
		"question_group_id":"acceptance-demo","question_index":1,"question_total":3,
		"options":"[{\"id\":\"remote\",\"label\":\"远程办公\"},{\"id\":\"office\",\"label\":\"到办公室\"}]",
		"allow_other":"true","allow_skip":"false"
	}`)
	tool := NewAskUserTool(nil)
	cast := CastParams(args, tool.Parameters())
	var input AskUserInput
	if err := json.Unmarshal(cast, &input); err != nil {
		t.Fatalf("cast args = %s, schema = %s, error = %v", cast, tool.Parameters(), err)
	}
	if len(input.Options) != 2 || input.AllowOther == nil || !*input.AllowOther || input.AllowSkip == nil || *input.AllowSkip {
		t.Fatalf("cast input = %+v", input)
	}
}

func TestAskUserDefaultsOmittedQuestionIndex(t *testing.T) {
	args := json.RawMessage(`{
		"question":"你目前最需要解决什么问题？","mode":"single_choice",
		"question_group_id":"intake","question_total":3,
		"options":[{"id":"advice","label":"获取建议"},{"id":"action","label":"采取行动"}]
	}`)
	tool := NewAskUserTool(&fakeUserInputRequester{result: userinput.Result{Status: userinput.StatusSkipped}})
	if errs := ValidateParams(args, tool.Parameters()); len(errs) != 0 {
		t.Fatalf("omitted progress metadata failed schema validation: %+v", errs)
	}
	ctx, _ := askUserContext()
	result, err := tool.Execute(ctx, args)
	if err != nil || !result.Success {
		t.Fatalf("Execute() result = %+v, error = %v", result, err)
	}
	if tool.requester.(*fakeUserInputRequester).req.Question.Index != 1 ||
		tool.requester.(*fakeUserInputRequester).req.Question.Total != 3 {
		t.Fatalf("default progress = %+v", tool.requester.(*fakeUserInputRequester).req.Question)
	}
}

func TestAskUserUsesRequestIDFromContext(t *testing.T) {
	requester := &fakeUserInputRequester{result: userinput.Result{Status: userinput.StatusSkipped}}
	base := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	base = context.WithValue(base, types.RequestIDContextKey, "request-from-context")
	ctx := WithToolExecContext(base, &ToolExecContext{
		SessionID: "session-1", AssistantMessageID: "message-1", ToolCallID: "tool-1",
		UserID: "user-1", EventBus: event.NewEventBus(), ApprovalCtx: base,
	})
	_, _ = NewAskUserTool(requester).Execute(ctx, askUserArgs())
	if requester.req.RequestID != "request-from-context" {
		t.Fatalf("request id = %q", requester.req.RequestID)
	}
}

func TestAskUserReturnsTerminalStatuses(t *testing.T) {
	for _, status := range []userinput.Status{userinput.StatusSkipped, userinput.StatusTimedOut, userinput.StatusCanceled} {
		t.Run(string(status), func(t *testing.T) {
			requester := &fakeUserInputRequester{result: userinput.Result{Status: status}}
			ctx, _ := askUserContext()
			result, err := NewAskUserTool(requester).Execute(ctx, askUserArgs())
			if err != nil || !result.Success || !json.Valid([]byte(result.Output)) {
				t.Fatalf("Execute() result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestAskUserRejectsInvalidInputAndMissingMetadata(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		args json.RawMessage
	}{
		{name: "invalid json", ctx: context.Background(), args: json.RawMessage(`{"question":`)},
		{name: "missing execution metadata", ctx: context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000)), args: askUserArgs()},
		{name: "missing tenant", ctx: WithToolExecContext(context.Background(), &ToolExecContext{UserID: "user-1", EventBus: event.NewEventBus()}), args: askUserArgs()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requester := &fakeUserInputRequester{}
			result, err := NewAskUserTool(requester).Execute(tt.ctx, tt.args)
			if err != nil || result.Success || result.Error == "" || requester.calls != 0 {
				t.Fatalf("Execute() result = %+v, error = %v, calls = %d", result, err, requester.calls)
			}
		})
	}
}

func TestAskUserSurfacesRequesterError(t *testing.T) {
	requester := &fakeUserInputRequester{err: errors.New("wait failed")}
	ctx, _ := askUserContext()
	result, err := NewAskUserTool(requester).Execute(ctx, askUserArgs())
	if err == nil || result.Success || result.Error != "wait failed" {
		t.Fatalf("Execute() result = %+v, error = %v", result, err)
	}
}
