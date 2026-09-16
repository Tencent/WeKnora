package invoke

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineStoredImages(t *testing.T) {
	orig := LocalImageResolver
	t.Cleanup(func() { LocalImageResolver = orig })
	LocalImageResolver = func(url string) ([]byte, bool) {
		if url == "resource://resolvable" {
			return []byte{0x89, 'P', 'N', 'G'}, true
		}
		return nil, false
	}

	dataURI := "data:image/png;base64,aGVsbG8="
	messages := []Message{
		TextMessage(RoleSystem, "sys"),
		{Role: RoleUser, Content: []Part{
			{Text: "look"},
			{Image: &ImageRef{URL: "resource://resolvable", Detail: "auto"}},
			{Image: &ImageRef{URL: dataURI}},
			{Image: &ImageRef{URL: "https://example.com/x.png"}},
			{Image: &ImageRef{URL: "resource://missing"}},
		}},
	}

	got := inlineStoredImages(messages)

	require.Len(t, got, 2)
	parts := got[1].Content
	require.Len(t, parts, 5)
	assert.Equal(t, "data:image/png;base64,", parts[1].Image.URL[:len("data:image/png;base64,")],
		"stored reference must be inlined as a data URI")
	assert.Equal(t, "auto", parts[1].Image.Detail, "detail rides the rewritten ref")
	assert.Same(t, messages[1].Content[2].Image, parts[2].Image, "data URI passes through untouched")
	assert.Same(t, messages[1].Content[3].Image, parts[3].Image, "http URL passes through untouched")
	assert.Equal(t, "resource://missing", parts[4].Image.URL,
		"unresolvable stored reference stays as-is so the vendor error stays loud")

	// The input slice must not be mutated (the caller owns opts.Messages).
	assert.Equal(t, "resource://resolvable", messages[1].Content[1].Image.URL)
}

func TestInlineStoredImagesPassthroughWithoutHookOrNeed(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: []Part{
			{Text: "plain"},
			{Image: &ImageRef{URL: "data:image/png;base64,aGVsbG8="}},
		}},
	}

	hook := LocalImageResolver
	t.Cleanup(func() { LocalImageResolver = hook })
	LocalImageResolver = nil
	got := inlineStoredImages(messages)
	assert.True(t, &got[0] == &messages[0], "no hook: zero-copy passthrough")

	LocalImageResolver = func(string) ([]byte, bool) { return []byte{1}, true }
	got = inlineStoredImages(messages)
	assert.True(t, &got[0] == &messages[0], "nothing to inline: zero-copy passthrough")

	stored := []Message{{Role: RoleUser, Content: []Part{
		{Image: &ImageRef{URL: "local://10000/exports/x.png"}},
	}}}
	got = inlineStoredImages(stored)
	assert.True(t, IsApplicationStoredImage("local://10000/exports/x.png"),
		"local:// must be classified as a stored scheme")
	assert.Equal(t, "data:image/png;base64,", got[0].Content[0].Image.URL[:len("data:image/png;base64,")],
		"local:// must be inlined")
}
