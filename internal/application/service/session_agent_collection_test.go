package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type scriptedCollectionRequester struct {
	questions []userinput.Question
	owners    []string
	status    userinput.Status
}

func (r *scriptedCollectionRequester) RequestAndWait(
	_ context.Context,
	req userinput.PendingRequest,
) (userinput.Result, error) {
	r.questions = append(r.questions, req.Question)
	r.owners = append(r.owners, req.UserID)
	status := r.status
	if status == "" {
		status = userinput.StatusAnswered
	}
	result := userinput.Result{
		Status: status, FieldKey: req.Question.FieldKey, SchemaVersion: req.Question.SchemaVersion,
		QuestionGroupID: req.Question.GroupID, QuestionIndex: req.Question.Index, QuestionTotal: req.Question.Total,
	}
	if status != userinput.StatusAnswered {
		return result, nil
	}
	switch req.Question.Mode {
	case userinput.ModeSingle:
		result.SelectedOptions = []userinput.Option{req.Question.Options[0]}
	case userinput.ModeMultiple:
		result.SelectedOptions = append([]userinput.Option(nil), req.Question.Options...)
	case userinput.ModeNumber:
		result.Value = float64(len(r.questions))
	case userinput.ModeDate:
		result.Value = "2026-07-22"
	default:
		result.Value = "回答-" + req.Question.FieldKey
	}
	return result, nil
}

func TestSessionAgentCollectionUsesAuthenticatedPrincipalForQuestionOwner(t *testing.T) {
	requester := &scriptedCollectionRequester{}
	service := newSessionCollectionService(t, requester)
	req := sessionCollectionRequest(fourFieldCollectionConfig())
	req.Session.UserID = "user-1"
	ctx := types.WithPrincipal(context.Background(), types.Principal{
		Type: types.PrincipalWebUser,
		ID:   "user-1",
	})

	require.NoError(t, service.prepareAgentCollection(ctx, req, nil, event.NewEventBus()))
	require.NotEmpty(t, requester.owners)
	require.Equal(t, "web_user:user-1", requester.owners[0])
}

func newSessionCollectionService(t *testing.T, requester userinput.Requester) *sessionService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.AgentCollectionProfile{}, &types.AgentCollectionHistory{}, &types.AgentCollectionExport{},
	))
	collection := NewAgentCollectionService(repository.NewAgentCollectionRepository(db))
	return &sessionService{agentCollectionService: collection, userInputRequester: requester}
}

func sessionCollectionRequest(config types.CustomAgentConfig) *types.QARequest {
	return &types.QARequest{
		Session: &types.Session{ID: "session-1", TenantID: 8, UserID: "web_user:user-1"},
		Query:   "我需要咨询", AssistantMessageID: "assistant-1", UserMessageID: "user-message-1",
		CustomAgent: &types.CustomAgent{ID: "agent-1", TenantID: 7, Config: config}, Channel: types.ChannelWeb,
	}
}

func fourFieldCollectionConfig() types.CustomAgentConfig {
	fields := make([]types.AgentCollectionField, 4)
	for index := range fields {
		fields[index] = types.AgentCollectionField{
			Key: fmt.Sprintf("field_%d", index+1), Label: fmt.Sprintf("问题 %d", index+1),
			Type: types.AgentCollectionShortText, Required: true, Enabled: true, Order: index + 1,
		}
	}
	return types.CustomAgentConfig{
		CollectionEnabled: true, CollectionSchemaVersion: 1,
		CollectionExtractionThreshold: 0.85, CollectionFields: fields,
	}
}

func TestSessionAgentCollectionAsksMoreThanThreeDynamicQuestions(t *testing.T) {
	requester := &scriptedCollectionRequester{}
	service := newSessionCollectionService(t, requester)
	err := service.prepareAgentCollection(
		context.Background(), sessionCollectionRequest(fourFieldCollectionConfig()), nil, event.NewEventBus(),
	)
	require.NoError(t, err)
	require.Len(t, requester.questions, 4)
	for index, question := range requester.questions {
		require.Equal(t, 4-index, question.RemainingCount)
		require.Equal(t, index+1, question.Index)
		require.Equal(t, 4, question.Total)
	}

	err = service.prepareAgentCollection(
		context.Background(), sessionCollectionRequest(fourFieldCollectionConfig()), nil, event.NewEventBus(),
	)
	require.NoError(t, err)
	require.Len(t, requester.questions, 4, "completed profile must not prompt again")
}

func TestSessionAgentCollectionExtractsBeforePrompting(t *testing.T) {
	requester := &scriptedCollectionRequester{}
	service := newSessionCollectionService(t, requester)
	config := fourFieldCollectionConfig()
	config.CollectionExtractFromMessages = true
	model := &collectionExtractorChat{content: `{"updates":[{
		"field_key":"field_1","value":"已说明","confidence":0.97,"evidence":"已说明"
	}]}`}
	req := sessionCollectionRequest(config)
	req.Query = "第一项已说明"

	require.NoError(t, service.prepareAgentCollection(context.Background(), req, model, event.NewEventBus()))
	require.Len(t, requester.questions, 3)
	require.Equal(t, "field_2", requester.questions[0].FieldKey)
}

func TestSessionAgentCollectionSkipsNonWebAndStopsOnTimeout(t *testing.T) {
	requester := &scriptedCollectionRequester{status: userinput.StatusTimedOut}
	service := newSessionCollectionService(t, requester)
	req := sessionCollectionRequest(fourFieldCollectionConfig())
	req.Channel = "api"
	require.NoError(t, service.prepareAgentCollection(context.Background(), req, nil, event.NewEventBus()))
	require.Empty(t, requester.questions)

	req.Channel = types.ChannelWeb
	err := service.prepareAgentCollection(context.Background(), req, nil, event.NewEventBus())
	require.True(t, errors.Is(err, ErrAgentCollectionInterrupted))
	require.Len(t, requester.questions, 1)
}

func TestSessionAgentCollectionFallsBackWhenExtractionFails(t *testing.T) {
	requester := &scriptedCollectionRequester{}
	service := newSessionCollectionService(t, requester)
	config := fourFieldCollectionConfig()
	config.CollectionExtractFromMessages = true
	model := &collectionExtractorChat{err: errors.New("rate limited")}

	require.NoError(t, service.prepareAgentCollection(
		context.Background(), sessionCollectionRequest(config), model, event.NewEventBus(),
	))
	require.Len(t, requester.questions, 4)
}

func TestSessionAgentCollectionNaturallyAsksOneRelevantOptionalField(t *testing.T) {
	requester := &scriptedCollectionRequester{}
	service := newSessionCollectionService(t, requester)
	config := types.CustomAgentConfig{
		CollectionEnabled: true, CollectionSchemaVersion: 1,
		CollectionExtractFromMessages: true, CollectionExtractionThreshold: 0.85,
		CollectionCollectOptionalDuringIntake: false,
		CollectionFields: []types.AgentCollectionField{{
			Key: "years", Label: "工作年限", Description: "用于判断补偿标准",
			Type: types.AgentCollectionNumber, Required: false, Enabled: true,
		}},
	}
	model := &collectionExtractorChat{content: `{
		"updates":[],
		"follow_up":{
			"field_key":"years",
			"question":"为了更准确地判断补偿标准，方便说一下工作几年了吗？",
			"confidence":0.94
		}
	}`}
	req := sessionCollectionRequest(config)
	req.Query = "被辞退了怎么办"

	require.NoError(t, service.prepareAgentCollection(context.Background(), req, model, event.NewEventBus()))
	require.Len(t, requester.questions, 1)
	require.Equal(t, "years", requester.questions[0].FieldKey)
	require.Contains(t, requester.questions[0].Text, "方便说一下")
	require.True(t, requester.questions[0].AllowSkip)

	require.NoError(t, service.prepareAgentCollection(context.Background(), req, model, event.NewEventBus()))
	require.Len(t, requester.questions, 1, "an answered optional field must not be asked again")
}
