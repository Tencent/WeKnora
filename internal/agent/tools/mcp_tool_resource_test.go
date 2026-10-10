package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/stretchr/testify/assert"
)

// The text of an embedded resource reaches the model after a reference to the
// resource; links and binary resources stay references.
func TestExtractContentAndImages_Resources(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "text", Text: "successfully downloaded text file"},
		{Type: "resource", URI: "repo://octo/app/contents/main.go", MimeType: "text/x-go", Text: "package main"},
		{Type: "resource", URI: "repo://octo/app/contents/logo.png", MimeType: "image/png", Data: "iVBORw0KGgo="},
		{Type: "resource_link", URI: "repo://octo/app/contents/big.log", MimeType: "text/plain", Text: "big.log"},
	}

	text, images, skipped := extractContentAndImages(content)
	assert.Equal(t, strings.Join([]string{
		"successfully downloaded text file",
		"[Resource: repo://octo/app/contents/main.go (text/x-go)]",
		"package main",
		"[Resource: repo://octo/app/contents/logo.png (image/png)]",
		"[Resource link: repo://octo/app/contents/big.log (text/plain)]",
	}, "\n"), text)
	assert.Empty(t, images)
	assert.Zero(t, skipped)

	assert.Equal(t, text, extractContentText(content), "the error path renders resources the same way")
}

// Base64 payloads of any kind are kept out of the logged content items.
func TestRedactImageData_BlobResourcesAndAudio(t *testing.T) {
	redacted := redactImageData([]mcp.ContentItem{
		{Type: "resource", MimeType: "image/png", Data: "iVBORw0KGgo="},
		{Type: "audio", MimeType: "audio/wav", Data: "YXVkaW8="},
		{Type: "resource", MimeType: "text/plain", Text: "kept"},
	})
	assert.Equal(t, "[redacted, base64_len=12]", redacted[0].Data)
	assert.Equal(t, "[redacted, base64_len=8]", redacted[1].Data)
	assert.Equal(t, "kept", redacted[2].Text)
}
