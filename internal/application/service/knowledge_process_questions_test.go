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

func TestKeepQuestionsGroundedInChunk(t *testing.T) {
	const (
		current = "瑶王五桶汤"
		prev    = "东兰县水司令桶装水"
		next    = "邱氏烧卤"
	)
	got := keepQuestionsGroundedInChunk([]string{
		"东兰县水司令多少钱",
		"五桶汤怎么做",
		"这道菜怎么做",
		"邱氏烧卤要多久",
		"瑶王五桶汤和东兰县水司令有什么关系",
	}, current, prev, next)
	want := []string{
		"五桶汤怎么做",
		"这道菜怎么做",
		"瑶王五桶汤和东兰县水司令有什么关系",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %#v, want %#v", got, want)
	}
}

func TestKeepQuestionsGroundedInChunkKeepsParaphraseWithoutNeighbors(t *testing.T) {
	got := keepQuestionsGroundedInChunk([]string{"How is this chunk indexed?"}, "chunk index", "", "")
	want := []string{"How is this chunk indexed?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %#v, want %#v", got, want)
	}
}

func TestKeepQuestionsGroundedInChunkKeepsSharedASCIIToken(t *testing.T) {
	got := keepQuestionsGroundedInChunk(
		[]string{"Where is the refund policy stored?"},
		"The refund policy applies to annual plans.",
		"Shipping windows are listed separately.",
		"",
	)
	want := []string{"Where is the refund policy stored?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %#v, want %#v", got, want)
	}
}

func TestKeepQuestionsGroundedInChunkDropsNeighborOnlyASCII(t *testing.T) {
	got := keepQuestionsGroundedInChunk(
		[]string{"When does shipping close?"},
		"The refund policy applies to annual plans.",
		"Shipping windows close at noon.",
		"",
	)
	if len(got) != 0 {
		t.Fatalf("questions = %#v, want none", got)
	}
}
