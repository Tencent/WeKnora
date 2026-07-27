package userinput

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
)

func eventOptions(options []Option) []event.UserInputOptionData {
	result := make([]event.UserInputOptionData, 0, len(options))
	for _, option := range options {
		result = append(result, event.UserInputOptionData{
			ID: option.ID, Label: option.Label, Description: option.Description,
		})
	}
	return result
}

func (g *Gate) emitRequired(ctx context.Context, pendingID string, req PendingRequest) error {
	timeoutSeconds := int(g.timeout / time.Second)
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	q := req.Question
	err := req.EventBus.Emit(ctx, event.Event{
		ID: pendingID + "-user-input-required", Type: event.EventUserInputRequired,
		SessionID: req.SessionID, RequestID: req.RequestID,
		Data: event.UserInputRequiredData{
			PendingID: pendingID, TenantID: req.TenantID, SessionID: req.SessionID,
			AssistantMessageID: req.AssistantMessageID, ToolCallID: req.ToolCallID,
			RequestID: req.RequestID, Question: q.Text, Mode: string(q.Mode),
			FieldKey: q.FieldKey, SchemaVersion: q.SchemaVersion,
			QuestionGroupID: q.GroupID, QuestionIndex: q.Index, QuestionTotal: q.Total,
			CompletedCount: q.CompletedCount, RemainingCount: q.RemainingCount,
			Options: eventOptions(q.Options), AllowOther: q.AllowOther, AllowSkip: q.AllowSkip,
			Validation:     q.Validation,
			TimeoutSeconds: timeoutSeconds, RequestedAtUnix: time.Now().Unix(),
		},
		Metadata: map[string]interface{}{
			"assistant_message_id": req.AssistantMessageID, "pending_id": pendingID,
		},
	})
	if err != nil {
		return fmt.Errorf("emit user input required: %w", err)
	}
	return nil
}

func (g *Gate) emitResolved(ctx context.Context, pendingID string, req PendingRequest, result Result) {
	_ = req.EventBus.Emit(context.WithoutCancel(ctx), event.Event{
		ID: pendingID + "-user-input-resolved", Type: event.EventUserInputResolved,
		SessionID: req.SessionID, RequestID: req.RequestID,
		Data: event.UserInputResolvedData{
			PendingID: pendingID, Status: string(result.Status), QuestionGroupID: result.QuestionGroupID,
			FieldKey: result.FieldKey, SchemaVersion: result.SchemaVersion,
			QuestionIndex: result.QuestionIndex, QuestionTotal: result.QuestionTotal,
			CompletedCount: req.Question.CompletedCount, RemainingCount: req.Question.RemainingCount,
			SelectedOptions: eventOptions(result.SelectedOptions), OtherText: result.OtherText,
			Value: result.Value, Reason: result.Reason,
		},
		Metadata: map[string]interface{}{"assistant_message_id": req.AssistantMessageID},
	})
}
