package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// "Check the DocReader logs" must only appear for engines whose readers run
// inside the docreader container. Go-native engines call their APIs from
// this process; the docreader container has zero evidence of the request.
func TestParseFailureMessageForEngine(t *testing.T) {
	upstream := errors.New(`MinerU API status 400: {"error":"Unsupported file type: .pptx"}`)

	for _, engine := range []string{"", "builtin", "markitdown"} {
		assert.Empty(t, parseFailureMessageForEngine(engine, upstream),
			"engine %q runs in the docreader container; the docreader message applies", engine)
	}

	for _, engine := range []string{"mineru", "mineru_cloud", "weknoracloud", "anydoc", "simple", "paddleocr_vl"} {
		msg := parseFailureMessageForEngine(engine, upstream)
		assert.Contains(t, msg, engine, "engine %q must be named in the message", engine)
		assert.Contains(t, msg, "Unsupported file type", "the upstream error must surface")
		assert.NotContains(t, msg, "DocReader logs", "no misdirection to an unrelated component")
	}
}
