package service

import (
	"reflect"
	"testing"
)

func TestParseGeneratedQuestions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		count   int
		want    []string
	}{
		{
			name:    "numbered list",
			content: "1. What is WeKnora?\n2) How are chunks indexed?\n- ok\n",
			count:   3,
			want:    []string{"What is WeKnora?", "How are chunks indexed?"},
		},
		{
			name:    "limited to question count",
			content: "First question?\nSecond question?\nThird question?",
			count:   2,
			want:    []string{"First question?", "Second question?"},
		},
		{
			name: "inline think block is not parsed as questions",
			content: "<think>\nThe chunk talks about indexing.\nI should ask about it.\n</think>\n" +
				"What is WeKnora?\nHow are chunks indexed?",
			count: 3,
			want:  []string{"What is WeKnora?", "How are chunks indexed?"},
		},
		{
			name:    "unterminated think block yields nothing",
			content: "<think>\nThe chunk talks about indexing.\nI should ask about it.",
			count:   3,
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGeneratedQuestions(tt.content, tt.count)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("questions = %q, want %q", got, tt.want)
			}
		})
	}
}
