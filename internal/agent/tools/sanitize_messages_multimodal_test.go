package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeMessagesPreservesMultimodalContent(t *testing.T) {
	image := chat.MessageContentPart{
		Type: "image_url", ImageURL: &chat.ImageURL{URL: "https://example.com/a.png", Detail: "high"},
	}
	text := chat.MessageContentPart{Type: "text", Text: "describe this"}
	for i, msg := range []chat.Message{
		{Role: "user", Images: []string{"https://example.com/a.png"}},
		{Role: "user", MultiContent: []chat.MessageContentPart{image}},
		{Role: "user", MultiContent: []chat.MessageContentPart{text, image}},
	} {
		t.Run([]string{"legacy image", "image part", "text and image parts"}[i], func(t *testing.T) {
			got := SanitizeMessages([]chat.Message{msg})
			require.Equal(t, []chat.Message{msg}, got, "nonempty multimodal content must not be discarded")
		})
	}
}

func TestSanitizeMessagesKeepsImagesInProviderRequest(t *testing.T) {
	client := openaicompletions.New(openaicompletions.Config{
		Endpoint: api.Endpoint{BaseURL: "https://example.com/v1", Model: "vision-model"},
		Settings: api.DefaultOpenAICompletions(),
	})
	for _, stream := range []bool{false, true} {
		body, err := client.BuildRequestBody(SanitizeMessages([]chat.Message{
			{Role: "user", Content: "earlier conversation summary"},
			{Role: "user", MultiContent: []chat.MessageContentPart{
				{Type: "text", Text: "compare the screenshots"},
				{Type: "image_url", ImageURL: &chat.ImageURL{URL: "https://example.com/first.png", Detail: "high"}},
			}},
			{Role: "user", Content: "second screenshot", Images: []string{"https://example.com/second.png"}},
		}), nil, stream)
		require.NoError(t, err)
		data, err := json.Marshal(body)
		require.NoError(t, err)
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(data, &request))
		require.Len(t, request.Messages, 1)
		assert.Equal(t, "user", request.Messages[0].Role)
		assert.JSONEq(t, `[
			{"type":"text","text":"earlier conversation summary"},
			{"type":"text","text":"\n\n"},
			{"type":"text","text":"compare the screenshots"},
			{"type":"image_url","image_url":{"url":"https://example.com/first.png","detail":"high"}},
			{"type":"text","text":"\n\n"},
			{"type":"image_url","image_url":{"url":"https://example.com/second.png","detail":"auto"}},
			{"type":"text","text":"second screenshot"}
		]`, string(request.Messages[0].Content))
	}
}

func TestSanitizeMessagesMergesMultimodalContentInOrder(t *testing.T) {
	imageA := chat.MessageContentPart{
		Type: "image_url", ImageURL: &chat.ImageURL{URL: "https://example.com/a.png", Detail: "auto"},
	}
	imageB := chat.MessageContentPart{
		Type: "image_url", ImageURL: &chat.ImageURL{URL: "https://example.com/b.png", Detail: "high"},
	}
	legacyB := chat.MessageContentPart{
		Type: "image_url", ImageURL: &chat.ImageURL{URL: imageB.ImageURL.URL, Detail: "auto"},
	}
	textA := chat.MessageContentPart{Type: "text", Text: "first image"}
	textB := chat.MessageContentPart{Type: "text", Text: "second image"}
	separator := chat.MessageContentPart{Type: "text", Text: "\n\n"}
	cases := []struct {
		name     string
		messages []chat.Message
		want     []chat.MessageContentPart
	}{
		{"tool image messages", []chat.Message{
			{Role: "user", Content: textA.Text, Images: []string{imageA.ImageURL.URL}},
			{Role: "user", Content: textB.Text, Images: []string{imageB.ImageURL.URL}},
		}, []chat.MessageContentPart{imageA, textA, separator, legacyB, textB}},
		{"summary before multimodal user", []chat.Message{
			{Role: "user", Content: "compaction summary"},
			{Role: "user", MultiContent: []chat.MessageContentPart{textB, imageB}},
		}, []chat.MessageContentPart{{Type: "text", Text: "compaction summary"}, separator, textB, imageB}},
		{"text after multimodal user", []chat.Message{
			{Role: "user", MultiContent: []chat.MessageContentPart{textB, imageB}},
			{Role: "user", Content: "follow-up"},
		}, []chat.MessageContentPart{textB, imageB, separator, {Type: "text", Text: "follow-up"}}},
		{"mixed representations", []chat.Message{
			{Role: "user", Content: textA.Text, Images: []string{imageA.ImageURL.URL}},
			{Role: "user", Content: "fallback text", MultiContent: []chat.MessageContentPart{textB, imageB}},
			{Role: "user", Content: "compare them"},
		}, []chat.MessageContentPart{imageA, textA, separator, textB, imageB, separator, {
			Type: "text", Text: "compare them",
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeMessages(tc.messages)
			require.Len(t, got, 1)
			assert.Equal(t, tc.want, got[0].MultiContent)
			assert.Empty(t, got[0].Images, "images must not be sent twice by adapters reading both representations")
			assert.Empty(t, got[0].Content, "adapters must read all merged text from MultiContent")
			assert.Equal(t, got, SanitizeMessages(got), "sanitizing twice must preserve the payload")
		})
	}
}

func TestSanitizeMessagesDoesNotOverwriteMultimodalBackingArray(t *testing.T) {
	parts := make([]chat.MessageContentPart, 2, 4)
	parts[0] = chat.MessageContentPart{Type: "text", Text: "first"}
	parts[1] = chat.MessageContentPart{Type: "text", Text: "must stay"}
	messages := []chat.Message{
		{Role: "user", MultiContent: parts[:1]},
		{Role: "user", Content: "second"},
	}
	got := SanitizeMessages(messages)
	require.Len(t, got, 1)
	require.Len(t, got[0].MultiContent, 3)
	assert.Equal(t, "must stay", parts[1].Text)
	assert.Equal(t, "first", messages[0].MultiContent[0].Text)
	assert.Equal(t, "second", messages[1].Content)
}
