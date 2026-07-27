package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

var ErrAgentCollectionInterrupted = errors.New("agent information collection interrupted")

func (s *sessionService) prepareAgentCollection(
	ctx context.Context,
	req *types.QARequest,
	model chat.Chat,
	eventBus *event.EventBus,
) error {
	if !agentCollectionEnabled(req) {
		return nil
	}
	if s.agentCollectionService == nil || s.userInputRequester == nil || eventBus == nil {
		return fmt.Errorf("agent information collection is unavailable")
	}
	input, err := collectionPrepareInputFromRequest(ctx, req)
	if err != nil {
		return err
	}
	prepared, err := s.agentCollectionService.Prepare(ctx, input)
	if err != nil {
		return err
	}
	prepared, followUp := s.extractAgentCollection(ctx, req, input, prepared, model)
	if len(prepared.MissingFields) == 0 && !input.Config.CollectionCollectOptionalDuringIntake {
		smartInput := smartOptionalCollectionInput{
			req: req, collection: input, prepared: prepared, followUp: followUp, eventBus: eventBus,
		}
		if handled, err := s.askSmartOptionalCollection(ctx, smartInput); handled {
			return err
		}
	}
	return s.runCollectionQuestions(ctx, collectionQuestionRun{
		req: req, collection: input, prepared: prepared, eventBus: eventBus,
	})
}

func (s *sessionService) runCollectionQuestions(ctx context.Context, run collectionQuestionRun) error {
	skipped := make(map[string]struct{})
	for {
		field, completed, remaining, found := nextCollectionQuestion(run.prepared, skipped)
		if !found {
			return nil
		}
		question := collectionUserQuestion(field, run.prepared, completed, remaining)
		result, err := s.userInputRequester.RequestAndWait(ctx, userinput.PendingRequest{
			TenantID:           run.collection.TenantID,
			UserID:             collectionQuestionOwnerID(ctx, run.collection.UserID),
			SessionID:          run.req.Session.ID,
			AssistantMessageID: run.req.AssistantMessageID, RequestID: run.req.UserMessageID,
			ToolCallID: "collection-" + field.Key, EventBus: run.eventBus, Question: question,
		})
		if err != nil {
			return err
		}
		if result.Status == userinput.StatusSkipped && !field.Required {
			skipped[field.Key] = struct{}{}
			continue
		}
		if result.Status != userinput.StatusAnswered {
			return fmt.Errorf("%w: %s", ErrAgentCollectionInterrupted, result.Status)
		}
		if err := s.applyCollectionResult(ctx, collectionResultInput{
			req: run.req, collection: run.collection, field: field, result: result,
		}); err != nil {
			return err
		}
		run.prepared, err = s.agentCollectionService.Prepare(ctx, run.collection)
		if err != nil {
			return err
		}
	}
}

func collectionQuestionOwnerID(ctx context.Context, fallbackUserID string) string {
	if principal, ok := types.PrincipalFromContext(ctx); ok {
		return principal.StorageID()
	}
	return strings.TrimSpace(fallbackUserID)
}

func agentCollectionEnabled(req *types.QARequest) bool {
	return req != nil && req.Session != nil && req.CustomAgent != nil &&
		req.CustomAgent.Config.CollectionEnabled && interactiveUserInputEnabled(req.Channel)
}

func collectionPrepareInputFromRequest(
	ctx context.Context,
	req *types.QARequest,
) (types.PrepareCollectionInput, error) {
	userID := strings.TrimSpace(req.Session.UserID)
	if userID == "" {
		userID = sessionUserIDFromContext(ctx)
	}
	if req.Session.TenantID == 0 || req.CustomAgent.ID == "" || userID == "" {
		return types.PrepareCollectionInput{}, fmt.Errorf("collection tenant, agent, and session owner are required")
	}
	return types.PrepareCollectionInput{
		TenantID: req.Session.TenantID, AgentTenantID: req.CustomAgent.TenantID,
		AgentID: req.CustomAgent.ID, UserID: userID, Config: req.CustomAgent.Config,
	}, nil
}

func (s *sessionService) extractAgentCollection(
	ctx context.Context,
	req *types.QARequest,
	input types.PrepareCollectionInput,
	prepared *types.PreparedCollection,
	model chat.Chat,
) (*types.PreparedCollection, *CollectionFollowUp) {
	if !input.Config.CollectionExtractFromMessages || strings.TrimSpace(req.Query) == "" {
		return prepared, nil
	}
	turn, err := ExtractCollectionTurn(ctx, model, CollectionExtractionInput{
		Goal: input.Config.CollectionGoal, Fields: input.Config.CollectionFields,
		Current: prepared.Profile.Values, Message: req.Query,
	})
	if err != nil {
		logger.Warnf(ctx, "Agent collection extraction failed, continuing deterministic intake: %v", err)
		return prepared, nil
	}
	if len(turn.Updates) == 0 {
		return prepared, turn.FollowUp
	}
	messageAt := s.collectionSourceMessageTime(ctx, req)
	_, err = s.agentCollectionService.ApplyExtractedValues(ctx, types.ExtractedCollectionValuesInput{
		PrepareCollectionInput: input, Values: turn.Updates,
		SourceMessageID: req.UserMessageID, SourceMessageAt: messageAt,
	})
	if err != nil {
		logger.Warnf(ctx, "Agent collection extracted values were not applied: %v", err)
		return prepared, nil
	}
	refreshed, err := s.agentCollectionService.Prepare(ctx, input)
	if err != nil {
		logger.Warnf(ctx, "Agent collection refresh after extraction failed: %v", err)
		return prepared, nil
	}
	return refreshed, turn.FollowUp
}

func (s *sessionService) collectionSourceMessageTime(
	ctx context.Context,
	req *types.QARequest,
) *time.Time {
	if s.messageRepo != nil && req.UserMessageID != "" {
		message, err := s.messageRepo.GetMessage(ctx, req.Session.ID, req.UserMessageID)
		if err == nil && message != nil && !message.CreatedAt.IsZero() {
			createdAt := message.CreatedAt.UTC()
			return &createdAt
		}
	}
	now := time.Now().UTC()
	return &now
}

func nextCollectionQuestion(
	prepared *types.PreparedCollection,
	skipped map[string]struct{},
) (types.AgentCollectionField, int, int, bool) {
	remaining := 0
	var next types.AgentCollectionField
	found := false
	for _, field := range prepared.MissingFields {
		if _, wasSkipped := skipped[field.Key]; wasSkipped {
			continue
		}
		remaining++
		if !found {
			next, found = field, true
		}
	}
	completed := prepared.CompletedCount + len(skipped)
	return next, completed, remaining, found
}

func collectionUserQuestion(
	field types.AgentCollectionField,
	prepared *types.PreparedCollection,
	completed, remaining int,
) userinput.Question {
	options := make([]userinput.Option, 0, len(field.Options))
	for _, option := range field.Options {
		options = append(options, userinput.Option{ID: option.ID, Label: option.Label})
	}
	total := completed + remaining
	groupID := fmt.Sprintf("collection-%s-%d",
		strings.ReplaceAll(prepared.Profile.ID, "-", ""), prepared.Profile.SchemaVersion)
	return userinput.Question{
		Text: field.Label, Mode: collectionQuestionMode(field.Type),
		FieldKey: field.Key, SchemaVersion: prepared.Profile.SchemaVersion,
		GroupID: groupID, Index: completed + 1, Total: total,
		CompletedCount: completed, RemainingCount: remaining,
		Options: options, Validation: field.Validation, AllowSkip: !field.Required,
	}
}

func collectionQuestionMode(fieldType types.AgentCollectionFieldType) userinput.Mode {
	return map[types.AgentCollectionFieldType]userinput.Mode{
		types.AgentCollectionSingleChoice:   userinput.ModeSingle,
		types.AgentCollectionMultipleChoice: userinput.ModeMultiple,
		types.AgentCollectionShortText:      userinput.ModeShortText,
		types.AgentCollectionLongText:       userinput.ModeLongText,
		types.AgentCollectionNumber:         userinput.ModeNumber,
		types.AgentCollectionDate:           userinput.ModeDate,
	}[fieldType]
}

func collectionAnswerValue(
	field types.AgentCollectionField,
	result userinput.Result,
) (any, error) {
	if field.Type == types.AgentCollectionSingleChoice {
		if len(result.SelectedOptions) != 1 {
			return nil, fmt.Errorf("single-choice collection answer must contain one option")
		}
		return result.SelectedOptions[0].ID, nil
	}
	if field.Type == types.AgentCollectionMultipleChoice {
		values := make([]string, len(result.SelectedOptions))
		for index := range result.SelectedOptions {
			values[index] = result.SelectedOptions[index].ID
		}
		return values, nil
	}
	return result.Value, nil
}
