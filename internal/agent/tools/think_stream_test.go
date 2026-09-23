package tools

import "testing"

// feedAll runs the splitter over a sequence of chunks and concatenates the
// think / answer outputs (including the final Flush), mirroring how the agent
// stream router consumes it.
func feedAll(sp *ThinkStreamSplitter, chunks []string) (think, answer string) {
	for _, c := range chunks {
		tk, ans := sp.Feed(c)
		think += tk
		answer += ans
	}
	tk, ans := sp.Flush()
	think += tk
	answer += ans
	return think, answer
}

func TestThinkStreamSplitter(t *testing.T) {
	tests := []struct {
		name       string
		chunks     []string
		wantThink  string
		wantAnswer string
	}{
		{
			name:       "no think tags - all answer",
			chunks:     []string{"Hello ", "world"},
			wantThink:  "",
			wantAnswer: "Hello world",
		},
		{
			name:       "single think block then answer",
			chunks:     []string{"<think>reasoning</think>The answer is 42."},
			wantThink:  "reasoning",
			wantAnswer: "The answer is 42.",
		},
		{
			name:       "open tag split across chunks",
			chunks:     []string{"<thi", "nk>secret</think>visible"},
			wantThink:  "secret",
			wantAnswer: "visible",
		},
		{
			name:       "close tag split across chunks",
			chunks:     []string{"<think>think part</thi", "nk>answer part"},
			wantThink:  "think part",
			wantAnswer: "answer part",
		},
		{
			name:       "think content streamed across multiple chunks",
			chunks:     []string{"<think>a", "b", "c</think>", "done"},
			wantThink:  "abc",
			wantAnswer: "done",
		},
		{
			name:       "answer before think block",
			chunks:     []string{"prefix <think>mid</think> suffix"},
			wantThink:  "mid",
			wantAnswer: "prefix  suffix",
		},
		{
			name:       "two think blocks",
			chunks:     []string{"<think>one</think>A<think>two</think>B"},
			wantThink:  "onetwo",
			wantAnswer: "AB",
		},
		{
			name:       "unterminated think treated as think on flush",
			chunks:     []string{"<think>still thinking"},
			wantThink:  "still thinking",
			wantAnswer: "",
		},
		{
			name:       "literal less-than in answer is preserved",
			chunks:     []string{"if a < ", "b then"},
			wantThink:  "",
			wantAnswer: "if a < b then",
		},
		{
			name:       "empty chunks are no-ops",
			chunks:     []string{"", "answer", ""},
			wantThink:  "",
			wantAnswer: "answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := NewThinkStreamSplitter()
			think, answer := feedAll(sp, tt.chunks)
			if think != tt.wantThink {
				t.Errorf("think = %q, want %q", think, tt.wantThink)
			}
			if answer != tt.wantAnswer {
				t.Errorf("answer = %q, want %q", answer, tt.wantAnswer)
			}
		})
	}
}
