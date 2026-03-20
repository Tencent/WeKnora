package tools

import "testing"

func TestStripThinkBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no think blocks",
			input:    "Hello, world!",
			expected: "Hello, world!",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single think block",
			input:    "<think>Let me reason about this</think>The answer is 42.",
			expected: "The answer is 42.",
		},
		{
			name:     "multiline think block",
			input:    "<think>\nStep 1: analyze\nStep 2: solve\n</think>\nHere is my answer.",
			expected: "Here is my answer.",
		},
		{
			name:     "multiple think blocks",
			input:    "<think>first</think>Answer part 1. <think>second</think>Answer part 2.",
			expected: "Answer part 1. Answer part 2.",
		},
		{
			name:     "only think block",
			input:    "<think>just thinking</think>",
			expected: "",
		},
		{
			name:     "think block with surrounding whitespace",
			input:    "  <think>reasoning</think>  Answer  ",
			expected: "Answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripThinkBlocks(tt.input)
			if result != tt.expected {
				t.Errorf("StripThinkBlocks(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
