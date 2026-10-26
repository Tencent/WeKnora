package tools

import (
	"strings"
	"testing"
)

func TestNonceThinkTagsAcrossEveryChunkBoundary(t *testing.T) {
	for _, tc := range []struct{ input, thought, answer string }{
		{"<think:6124c78e>private reasoning\n步骤</think:6124c78e>完整报告", "private reasoning\n步骤", "完整报告"},
		{"before<think:abcdef>one</think:abcdef>middle<think>two</think>after", "onetwo", "beforemiddleafter"},
		{"<think:6124c78e>unfinished", "unfinished", ""},
		{"<think:6124c78e>hidden</think:deadbeef>still hidden", "hidden</think:deadbeef>still hidden", ""},
		{"literal <think:invalid> example", "", "literal <think:invalid> example"},
		{"literal <think:ab> example", "", "literal <think:ab> example"},
		{
			"literal <think:0123456789abcdef0123456789abcdef0> example",
			"",
			"literal <think:0123456789abcdef0123456789abcdef0> example",
		},
	} {
		for split := 0; split <= len(tc.input); split++ {
			thought, answer := feedAll(NewThinkStreamSplitter(), []string{tc.input[:split], tc.input[split:]})
			if thought != tc.thought || answer != tc.answer {
				t.Fatalf(
					"split %d of %q: got (%q, %q), want (%q, %q)",
					split,
					tc.input,
					thought,
					answer,
					tc.thought,
					tc.answer,
				)
			}
		}
		if got := StripThinkBlocks(tc.input); got != trimWhitespace(tc.answer) {
			t.Fatalf("persisted answer %q differs from streamed answer %q", got, tc.answer)
		}
	}
}

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

func TestOrphanNonceThinkCloserRetractsPrefixAcrossEveryBoundary(t *testing.T) {
	input := "refs\n\n任务全部结束，先整理证据。Prepare complete summary.</think:6124c78e>## 进展\n\n真正的完整答案。"
	for split := 0; split <= len(input); split++ {
		sp := NewThinkStreamSplitter()
		var thought, answer string
		for _, chunk := range []string{input[:split], input[split:]} {
			tk, ans := sp.Feed(chunk)
			if sp.TakeAnswerReset() {
				answer = ""
			}
			thought += tk
			answer += ans
		}
		tk, ans := sp.Flush()
		thought += tk
		answer += ans
		if answer != "## 进展\n\n真正的完整答案。" || thought != "refs\n\n任务全部结束，先整理证据。Prepare complete summary." {
			t.Fatalf("split %d: thought=%q answer=%q", split, thought, answer)
		}
		if got := StripThinkBlocks(input); got != answer {
			t.Fatalf("history differs: %q", got)
		}
	}
}

func TestOrphanThinkLiteralExamplesStayAnswers(t *testing.T) {
	for _, input := range []string{
		"A normal answer with </think> in its explanation.",
		"~~~xml\n<think:6124c78e>example</think:6124c78e>\n~~~\nNormal answer.",
		"Use `</think:6124c78e>` as the literal closing tag.",
		"```xml\n<think:6124c78e>example</think:6124c78e>\n```\nNormal answer.",
		"Escaped \\</think:6124c78e> example.",
		"Invalid </think:ab> and </think:xyz12345> remain.",
		"Too long </think:0123456789abcdef0123456789abcdef0> remains.",
	} {
		for split := 0; split <= len(input); split++ {
			sp := NewThinkStreamSplitter()
			_, answer := feedAll(sp, []string{input[:split], input[split:]})
			if answer != input || sp.TakeAnswerReset() {
				t.Fatalf("split %d changed literal %q into %q", split, input, answer)
			}
		}
		if got := StripThinkBlocks(input); got != input {
			t.Fatalf("changed persisted literal: %q", got)
		}
	}
}

func TestOrphanThinkPreviewBoundDoesNotTruncateFinalReasoningOrAnswer(t *testing.T) {
	prefix := strings.Repeat("内部规划", 6000)
	sp := NewThinkStreamSplitter()
	_, first := sp.Feed(prefix)
	if first != prefix {
		t.Fatal("normal prefix should stream immediately")
	}
	thought, answer := sp.Feed("</think:6124c78e>complete answer")
	if !sp.TakeAnswerReset() || len(thought) > 16*1024 || answer != "complete answer" {
		t.Fatal("bad bounded recovery")
	}
	fullThought, fullAnswer := SplitThinkContent(prefix + "</think:6124c78e>complete answer")
	if fullThought != prefix || fullAnswer != answer {
		t.Fatal("full reconstruction was truncated")
	}
}
