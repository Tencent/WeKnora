package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGeneratedQuestionsJSON(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		count   int
		want    []string
	}{
		{
			name: "compact envelope", count: 3,
			content: `{"questions":["什么是运行时队列？","如何配置知识库？"]}`,
			want:    []string{"什么是运行时队列？", "如何配置知识库？"},
		},
		{
			name: "pretty printed array", count: 3,
			content: "[\n  \"What is WeKnora?\",\n  \"How are chunks indexed?\"\n]",
			want:    []string{"What is WeKnora?", "How are chunks indexed?"},
		},
		{
			name: "fenced JSON after reasoning", count: 3,
			content: "<think>Decide what to ask.</think>\n```json\n" +
				`{"questions":["What is WeKnora?"]}` + "\n```",
			want: []string{"What is WeKnora?"},
		},
		{
			name: "unlabelled fence", count: 1,
			content: "```\n[\"First question?\",\"Second question?\"]\n```",
			want:    []string{"First question?"},
		},
		{
			name: "unlabelled fence with CRLF", count: 1,
			content: "```\r\n[\"First question?\"]\r\n```",
			want:    []string{"First question?"},
		},
		{
			name: "skip nonstring items", count: 2,
			content: `{"questions":[{"type":"object"},null,42,true,"What is WeKnora?",` +
				`["Nested question?"],"How are chunks indexed?"]}`,
			want: []string{"What is WeKnora?", "How are chunks indexed?"},
		},
		{
			name: "JSON escapes stay in one question", count: 3,
			content: `{"questions":["How does \"RAG\"\nwork?"]}`,
			want:    []string{"How does \"RAG\"\nwork?"},
		},
		{name: "schema object", count: 3, content: `{"type":"object"}`, want: []string{}},
		{name: "wrong envelope field type", count: 3, content: `{"questions":{"type":"array"}}`, want: []string{}},
		{name: "empty envelope", count: 3, content: `{"questions":[]}`, want: []string{}},
		{name: "truncated JSON", count: 3, content: "{\n\"questions\": [\"What is WeKnora?\"", want: []string{}},
		{name: "trailing garbage", count: 3, content: `{"questions":["What is WeKnora?"]} garbage`, want: []string{}},
		{
			name: "plain bracket prefix", count: 3,
			content: "[RAG] How are chunks indexed?\n{name} is used for which setting?",
			want:    []string{"[RAG] How are chunks indexed?", "{name} is used for which setting?"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, parseGeneratedQuestions(tt.content, tt.count))
		})
	}
}

func TestGenerateQuestionsWithContextDecodesJSONCompletion(t *testing.T) {
	model := &budgetCaptureChatModel{response: `{"questions":[{"type":"object"},"什么是运行时队列？"]}`}
	questions, err := newQuestionBudgetService().generateQuestionsWithContext(
		context.Background(), model, "Runtime queue documentation.", "", "", "runtime.md", 3, "",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"什么是运行时队列？"}, questions)
}
