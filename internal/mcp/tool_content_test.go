package mcp

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// Tool results can carry more than text and images. GitHub's
// get_file_contents, for one, returns a short text message and the file as an
// embedded resource; dropping the resource left the agent with only the
// message.
func TestToolContentItems(t *testing.T) {
	items := toolContentItems([]mcp.Content{
		mcp.NewTextContent("successfully downloaded text file"),
		mcp.NewEmbeddedResource(mcp.TextResourceContents{
			URI:      "repo://octo/app/contents/main.go",
			MIMEType: "text/x-go",
			Text:     "package main",
		}),
		mcp.NewEmbeddedResource(mcp.BlobResourceContents{
			URI:      "repo://octo/app/contents/logo.png",
			MIMEType: "image/png",
			Blob:     "iVBORw0KGgo=",
		}),
		mcp.NewResourceLink("repo://octo/app/contents/big.log", "big.log", "", "text/plain"),
		mcp.NewImageContent("aW1n", "image/png"),
		mcp.NewAudioContent("YXVkaW8=", "audio/wav"),
	})

	require.Equal(t, []ContentItem{
		{Type: "text", Text: "successfully downloaded text file"},
		{Type: "resource", URI: "repo://octo/app/contents/main.go", MimeType: "text/x-go", Text: "package main"},
		{Type: "resource", URI: "repo://octo/app/contents/logo.png", MimeType: "image/png", Data: "iVBORw0KGgo="},
		{Type: "resource_link", URI: "repo://octo/app/contents/big.log", MimeType: "text/plain", Text: "big.log"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
		{Type: "audio", Data: "YXVkaW8=", MimeType: "audio/wav"},
	}, items)
}
