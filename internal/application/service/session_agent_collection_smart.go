package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type collectionQuestionRun struct {
	req        *types.QARequest
	collection types.PrepareCollectionInput
	prepared   *types.PreparedCollection
	eventBus   *event.EventBus
}

type smartOptionalCollectionInput struct {
	req        *types.QARequest
	collection types.PrepareCollectionInput
	prepared   *types.PreparedCollection
	followUp   *CollectionFollowUp
	eventBus   *event.EventBus
}

type collectionResultInput struct {
	req        *types.QARequest
	collection types.PrepareCollectionInput
	field      types.AgentCollectionField
	result     userinput.Result
}

func (s *sessionService) askSmartOptionalCollection(
	ctx context.Context,
	input smartOptionalCollectionInput,
) (bool, error) {
	field, ok := smartOptionalCollectionField(
		input.prepared, input.followUp, input.collection.Config.CollectionExtractionThreshold,
	)
	if !ok {
		return false, nil
	}
	completed, remaining := visibleCollectionProgress(input.prepared)
	question := collectionUserQuestion(field, input.prepared, completed, remaining)
	question.Text = input.followUp.Question
	result, err := s.userInputRequester.RequestAndWait(ctx, userinput.PendingRequest{
		TenantID:           input.collection.TenantID,
		UserID:             collectionQuestionOwnerID(ctx, input.collection.UserID),
		SessionID:          input.req.Session.ID,
		AssistantMessageID: input.req.AssistantMessageID, RequestID: input.req.UserMessageID,
		ToolCallID: "collection-" + field.Key, EventBus: input.eventBus, Question: question,
	})
	if err != nil || result.Status == userinput.StatusSkipped {
		return true, err
	}
	if result.Status != userinput.StatusAnswered {
		return true, fmt.Errorf("%w: %s", ErrAgentCollectionInterrupted, result.Status)
	}
	return true, s.applyCollectionResult(ctx, collectionResultInput{
		req: input.req, collection: input.collection, field: field, result: result,
	})
}

func smartOptionalCollectionField(
	prepared *types.PreparedCollection,
	followUp *CollectionFollowUp,
	threshold float64,
) (types.AgentCollectionField, bool) {
	if followUp == nil || followUp.Confidence < threshold {
		return types.AgentCollectionField{}, false
	}
	for _, field := range prepared.VisibleFields {
		_, hasValue := prepared.Profile.Values[field.Key]
		if field.Key == followUp.FieldKey && !field.Required && !hasValue {
			return field, true
		}
	}
	return types.AgentCollectionField{}, false
}

func visibleCollectionProgress(prepared *types.PreparedCollection) (int, int) {
	completed, remaining := 0, 0
	for _, field := range prepared.VisibleFields {
		if _, hasValue := prepared.Profile.Values[field.Key]; hasValue {
			completed++
		} else {
			remaining++
		}
	}
	return completed, remaining
}

func (s *sessionService) applyCollectionResult(
	ctx context.Context,
	input collectionResultInput,
) error {
	value, err := collectionAnswerValue(input.field, input.result)
	if err != nil {
		return err
	}
	_, err = s.agentCollectionService.ApplyStructuredAnswer(ctx, types.StructuredCollectionAnswerInput{
		PrepareCollectionInput: input.collection, FieldKey: input.field.Key,
		SchemaVersion: input.collection.Config.CollectionSchemaVersion, Value: value,
		SourceMessageID: input.req.UserMessageID,
	})
	return err
}
