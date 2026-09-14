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

// TestThinkStreamSplitterCodeRegions covers #3132: think-tag markup inside
// fenced code blocks or inline code spans is literal document content and must
// stay in the answer, no matter how the text is chunked.
func TestThinkStreamSplitterCodeRegions(t *testing.T) {
	tests := []struct {
		name       string
		chunks     []string
		wantThink  string
		wantAnswer string
	}{
		{
			name:       "close tag inside fenced code block stays answer",
			chunks:     []string{"<think>reason</think>doc\n```go\n</think>\n```\ntail"},
			wantThink:  "reason",
			wantAnswer: "doc\n```go\n</think>\n```\ntail",
		},
		{
			name:       "open tag inside fenced code block stays answer",
			chunks:     []string{"```go\n<think>\n```\nvisible"},
			wantThink:  "",
			wantAnswer: "```go\n<think>\n```\nvisible",
		},
		{
			name:       "tilde fence protects tags",
			chunks:     []string{"~~~\n</think>\n~~~\nvisible"},
			wantThink:  "",
			wantAnswer: "~~~\n</think>\n~~~\nvisible",
		},
		{
			name:       "inline code span protects tags",
			chunks:     []string{"use `<think>` and `</think>` in docs"},
			wantThink:  "",
			wantAnswer: "use `<think>` and `</think>` in docs",
		},
		{
			name:       "fence opener split across chunks",
			chunks:     []string{"text\n``", "`go\n</think>\n``", "`\ntail"},
			wantThink:  "",
			wantAnswer: "text\n```go\n</think>\n```\ntail",
		},
		{
			name:       "close tag split across chunks inside fence",
			chunks:     []string{"```go\n</thi", "nk>\n```\ntail"},
			wantThink:  "",
			wantAnswer: "```go\n</think>\n```\ntail",
		},
		{
			name:       "backtick run split across chunks inside span",
			chunks:     []string{"a `<think", ">` b"},
			wantThink:  "",
			wantAnswer: "a `<think>` b",
		},
		{
			name:       "real think block after fenced mention",
			chunks:     []string{"```\n</think>\n```\n<think>real</think>final"},
			wantThink:  "real",
			wantAnswer: "```\n</think>\n```\nfinal",
		},
		{
			name:       "unterminated fence consumes rest as answer",
			chunks:     []string{"<think>r</think>a\n```\n</think>\nmore"},
			wantThink:  "r",
			wantAnswer: "a\n```\n</think>\nmore",
		},
		{
			name:       "unterminated span consumes rest as answer",
			chunks:     []string{"<think>r</think>a `</think> b"},
			wantThink:  "r",
			wantAnswer: "a `</think> b",
		},
		{
			name:       "fence inside think block is just reasoning text",
			chunks:     []string{"<think>uses ``` fences\n</think>answer"},
			wantThink:  "uses ``` fences\n",
			wantAnswer: "answer",
		},
		{
			name:       "mid-line backticks are not a fence",
			chunks:     []string{"text ``` </think> ``` more"},
			wantThink:  "",
			wantAnswer: "text ``` </think> ``` more",
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

// TestThinkStreamSplitterChunkingStable asserts that the think/answer routing
// is identical no matter where the chunk boundaries fall.
func TestThinkStreamSplitterChunkingStable(t *testing.T) {
	cases := []struct {
		input     string
		wantThink string
		wantAns   string
	}{
		{"a `<think>` b", "", "a `<think>` b"},
		{"<think>a</think>b", "a", "b"},
		{"stray </think> stays", "", "stray </think> stays"},
		{"x `<think>` y ```\n</think>\n``` z", "", "x `<think>` y ```\n</think>\n``` z"},
		{"```\n<think>\n```\n<think>k</think>ok", "k", "```\n<think>\n```\nok"},
	}
	for _, tc := range cases {
		for _, size := range []int{1, 2, 3, 5, 7, 11, len(tc.input)} {
			if size <= 0 {
				continue
			}
			sp := NewThinkStreamSplitter()
			var think, answer string
			for start := 0; start < len(tc.input); start += size {
				end := start + size
				if end > len(tc.input) {
					end = len(tc.input)
				}
				tk, ans := sp.Feed(tc.input[start:end])
				think, answer = think+tk, answer+ans
			}
			tk, ans := sp.Flush()
			think, answer = think+tk, answer+ans
			if think != tc.wantThink || answer != tc.wantAns {
				t.Errorf("input %q chunk size %d: think=%q answer=%q, want think=%q answer=%q",
					tc.input, size, think, answer, tc.wantThink, tc.wantAns)
			}
		}
	}
}
