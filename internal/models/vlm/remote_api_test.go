package vlm

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
)

func TestShapeVLMTokenParameter(t *testing.T) {
	t.Run("legacy max_tokens remains unchanged", func(t *testing.T) {
		req := openai.ChatCompletionRequest{MaxTokens: 5000, Temperature: 0.1}

		shapeVLMTokenParameter(&req, "max_tokens")

		assert.Equal(t, 5000, req.MaxTokens)
		assert.Zero(t, req.MaxCompletionTokens)
		assert.Equal(t, float32(0.1), req.Temperature)
	})

	t.Run("max_completion_tokens removes legacy and sampling fields", func(t *testing.T) {
		req := openai.ChatCompletionRequest{MaxTokens: 5000, Temperature: 0.1}

		shapeVLMTokenParameter(&req, "max_completion_tokens")

		assert.Zero(t, req.MaxTokens)
		assert.Equal(t, 5000, req.MaxCompletionTokens)
		assert.Zero(t, req.Temperature)
	})
}
