package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type collectionExtractorChat struct {
	content  string
	err      error
	calls    int
	messages []chat.Message
}

func (c *collectionExtractorChat) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.calls++
	c.messages = append([]chat.Message(nil), messages...)
	if c.err != nil {
		return nil, c.err
	}
	return &types.ChatResponse{Content: c.content}, nil
}

func (c *collectionExtractorChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (c *collectionExtractorChat) GetModelName() string { return "collection-test" }
func (c *collectionExtractorChat) GetModelID() string   { return "collection-test" }

func extractorFields() []types.AgentCollectionField {
	return []types.AgentCollectionField{
		{Key: "status", Label: "状态", Type: types.AgentCollectionSingleChoice, Enabled: true,
			Options: []types.AgentCollectionOption{{ID: "dismissed", Label: "被辞退"}, {ID: "employed", Label: "在职"}}},
		{Key: "years", Label: "工作年限", Type: types.AgentCollectionNumber, Enabled: true},
	}
}

func TestExtractCollectionValuesParsesStrictAndFencedJSON(t *testing.T) {
	for _, content := range []string{
		`{"updates":[{"field_key":"status","value":"dismissed","confidence":0.96,"evidence":"我被辞退了"}]}`,
		"```json\n{\"updates\":[{\"field_key\":\"status\",\"value\":\"dismissed\",\"confidence\":0.96,\"evidence\":\"我被辞退了\"}]}\n```",
	} {
		model := &collectionExtractorChat{content: content}
		got, err := ExtractCollectionValues(
			context.Background(), model, "了解劳动关系", extractorFields(), nil, "我被辞退了",
		)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, "status", got[0].FieldKey)
		require.Equal(t, "dismissed", got[0].Value)
	}
}

func TestExtractCollectionValuesDropsUnknownInvalidAndBadConfidence(t *testing.T) {
	model := &collectionExtractorChat{content: `{"updates":[
		{"field_key":"unknown","value":"x","confidence":0.99,"evidence":"三年"},
		{"field_key":"status","value":"other","confidence":0.99,"evidence":"三年"},
		{"field_key":"years","value":"three","confidence":0.99,"evidence":"三年"},
		{"field_key":"status","value":"employed","confidence":1.2,"evidence":"三年"},
		{"field_key":"years","value":3,"confidence":0.91,"evidence":"三年"}
	]}`}
	got, err := ExtractCollectionValues(
		context.Background(), model, "了解劳动关系", extractorFields(), types.JSONMap{}, "工作了三年",
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "years", got[0].FieldKey)
	require.Equal(t, float64(3), got[0].Value)
}

func TestExtractCollectionValuesShortCircuitsAndNeverFabricatesOnErrors(t *testing.T) {
	model := &collectionExtractorChat{content: `{"updates":[]}`}
	got, err := ExtractCollectionValues(context.Background(), model, "目标", nil, nil, "消息")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, model.calls)

	model = &collectionExtractorChat{err: errors.New("model unavailable")}
	got, err = ExtractCollectionValues(context.Background(), model, "目标", extractorFields(), nil, "消息")
	require.ErrorContains(t, err, "model unavailable")
	require.Empty(t, got)

	model = &collectionExtractorChat{content: `before {"updates":[]} after`}
	got, err = ExtractCollectionValues(context.Background(), model, "目标", extractorFields(), nil, "消息")
	require.Error(t, err)
	require.Empty(t, got)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = ExtractCollectionValues(canceled, &collectionExtractorChat{}, "目标", extractorFields(), nil, "消息")
	require.Error(t, err)
	require.Empty(t, got)
}

func TestExtractCollectionTurnSuggestsOneRelevantOptionalFollowUp(t *testing.T) {
	fields := extractorFields()
	fields[1].Description = "用于判断劳动争议补偿年限"
	model := &collectionExtractorChat{content: `{
		"updates":[],
		"follow_up":{
			"field_key":"years",
			"question":"为了更准确地判断补偿标准，方便说一下工作几年了吗？",
			"confidence":0.94
		}
	}`}

	turn, err := ExtractCollectionTurn(context.Background(), model, CollectionExtractionInput{
		Goal: "判断劳动争议处理方向", Fields: fields, Message: "被辞退了怎么办",
	})
	require.NoError(t, err)
	require.Empty(t, turn.Updates)
	require.NotNil(t, turn.FollowUp)
	require.Equal(t, "years", turn.FollowUp.FieldKey)
	require.Contains(t, turn.FollowUp.Question, "工作几年")
	require.Contains(t, model.messages[1].Content, `"required":false`)
	require.Contains(t, model.messages[1].Content, "用于判断劳动争议补偿年限")
}

func TestExtractCollectionTurnRejectsRequiredOrUnknownFollowUp(t *testing.T) {
	fields := extractorFields()
	fields[0].Required = true
	for _, fieldKey := range []string{"status", "unknown"} {
		model := &collectionExtractorChat{content: `{
			"updates":[],
			"follow_up":{"field_key":"` + fieldKey + `","question":"请补充信息","confidence":0.99}
		}`}
		turn, err := ExtractCollectionTurn(context.Background(), model, CollectionExtractionInput{
			Goal: "了解劳动关系", Fields: fields, Message: "我需要咨询",
		})
		require.NoError(t, err)
		require.Nil(t, turn.FollowUp)
	}
}
