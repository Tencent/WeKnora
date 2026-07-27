package userinput_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestAskUserToolBlocksUntilGateResolves(t *testing.T) {
	gate := userinput.NewGate(&config.Config{Agent: &config.AgentConfig{
		ToolApprovalTimeoutSeconds: 2,
	}}, nil)
	bus := event.NewEventBus()
	required := make(chan event.UserInputRequiredData, 1)
	resolved := make(chan event.UserInputResolvedData, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		required <- evt.Data.(event.UserInputRequiredData)
		return nil
	})
	bus.On(event.EventUserInputResolved, func(_ context.Context, evt event.Event) error {
		resolved <- evt.Data.(event.UserInputResolvedData)
		return nil
	})

	base := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	ctx := tools.WithToolExecContext(base, &tools.ToolExecContext{
		SessionID: "session-1", AssistantMessageID: "message-1", RequestID: "request-1",
		ToolCallID: "tool-1", UserID: "user-1", EventBus: bus, ApprovalCtx: base,
	})
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := tools.NewAskUserTool(gate).Execute(ctx, json.RawMessage(`{
			"question":"公司如何通知你解除劳动合同？","mode":"single_choice",
			"question_group_id":"dismissal-facts","question_index":1,"question_total":3,
			"options":[{"id":"written","label":"书面通知"},{"id":"verbal","label":"口头通知"}]
		}`))
		if result != nil {
			resultCh <- result.Output
		}
		errCh <- err
	}()

	question := <-required
	if question.QuestionIndex != 1 || question.QuestionTotal != 3 || len(question.Options) != 2 {
		t.Fatalf("required event = %+v", question)
	}
	select {
	case <-resultCh:
		t.Fatal("ask_user returned before the user answered")
	case <-time.After(20 * time.Millisecond):
	}
	if err := gate.Resolve(10000, "user-1", question.PendingID, userinput.Answer{
		OtherText: "  已收到解除通知书  ",
	}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var output userinput.Result
	if err := json.Unmarshal([]byte(<-resultCh), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.Status != userinput.StatusAnswered || output.OtherText != "已收到解除通知书" {
		t.Fatalf("tool output = %+v", output)
	}
	if terminal := <-resolved; terminal.PendingID != question.PendingID || terminal.Status != "answered" {
		t.Fatalf("resolved event = %+v", terminal)
	}
}
