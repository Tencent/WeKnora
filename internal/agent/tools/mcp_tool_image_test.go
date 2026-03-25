package tools

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
)

// testBase64PNG is a minimal valid base64-encoded 1x1 red PNG for testing.
var testBase64PNG = base64.StdEncoding.EncodeToString([]byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC,
	0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
	0x44, 0xAE, 0x42, 0x60, 0x82,
})

func TestExtractContentAndImages_TextOnly(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "hello") || !strings.Contains(text, "world") {
		t.Errorf("expected text to contain 'hello' and 'world', got: %s", text)
	}
	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
}

func TestExtractContentAndImages_ImageWithData(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "image", MimeType: "image/png", Data: testBase64PNG},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "[Image: image/png]") {
		t.Errorf("expected placeholder in text, got: %s", text)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if !strings.HasPrefix(images[0], "data:image/png;base64,") {
		t.Errorf("expected data URI prefix, got: %s", images[0][:40])
	}
}

func TestExtractContentAndImages_ImageWithoutData(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "image", MimeType: "image/jpeg", Data: ""},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "[Image: image/jpeg]") {
		t.Errorf("expected placeholder, got: %s", text)
	}
	if len(images) != 0 {
		t.Errorf("expected 0 images for empty data, got %d", len(images))
	}
}

func TestExtractContentAndImages_MixedContent(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "text", Text: "before image"},
		{Type: "image", MimeType: "image/png", Data: testBase64PNG},
		{Type: "text", Text: "after image"},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "before image") || !strings.Contains(text, "after image") {
		t.Errorf("expected text parts, got: %s", text)
	}
	if !strings.Contains(text, "[Image: image/png]") {
		t.Errorf("expected placeholder, got: %s", text)
	}
	if len(images) != 1 {
		t.Errorf("expected 1 image, got %d", len(images))
	}
}

func TestExtractContentAndImages_MIMEWhitelist(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "image", MimeType: "text/html", Data: testBase64PNG},
		{Type: "image", MimeType: "application/javascript", Data: testBase64PNG},
	}
	text, images, _ := extractContentAndImages(content)

	// Placeholders should still appear
	if !strings.Contains(text, "[Image: text/html]") {
		t.Errorf("expected placeholder for text/html, got: %s", text)
	}
	// But images should be rejected
	if len(images) != 0 {
		t.Errorf("expected 0 images for non-whitelisted MIME, got %d", len(images))
	}
}

func TestExtractContentAndImages_SizeLimit(t *testing.T) {
	// Create a base64 string that decodes to just over maxMCPImageSize (10MB).
	// Base64 encodes 3 bytes into 4 chars, so we need 4/3 * (10MB+1) chars.
	oversized := strings.Repeat("A", maxMCPImageSize*4/3+100)
	content := []mcp.ContentItem{
		{Type: "image", MimeType: "image/png", Data: oversized},
	}
	_, images, _ := extractContentAndImages(content)

	if len(images) != 0 {
		t.Errorf("expected 0 images for oversized data, got %d", len(images))
	}
}

func TestExtractContentAndImages_CountLimit(t *testing.T) {
	content := make([]mcp.ContentItem, 7)
	for i := range content {
		content[i] = mcp.ContentItem{Type: "image", MimeType: "image/png", Data: testBase64PNG}
	}
	_, images, skipped := extractContentAndImages(content)

	if len(images) != maxMCPImages {
		t.Errorf("expected %d images (max), got %d", maxMCPImages, len(images))
	}
	if skipped != 2 {
		t.Errorf("expected 2 skipped images, got %d", skipped)
	}
}

func TestExtractContentAndImages_DefaultMIME(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "image", MimeType: "", Data: testBase64PNG},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "[Image: image/png]") {
		t.Errorf("expected default mime in placeholder, got: %s", text)
	}
	if len(images) != 1 {
		t.Errorf("expected 1 image with default mime, got %d", len(images))
	}
}

func TestExtractContentAndImages_EmptyContent(t *testing.T) {
	text, images, _ := extractContentAndImages(nil)

	if text != "Tool executed successfully (no text output)" {
		t.Errorf("expected default text, got: %s", text)
	}
	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
}

func TestExtractContentAndImages_ResourceAndDefault(t *testing.T) {
	content := []mcp.ContentItem{
		{Type: "resource", MimeType: "application/json"},
		{Type: "unknown", Text: "some text"},
		{Type: "unknown", Data: "some data"},
	}
	text, images, _ := extractContentAndImages(content)

	if !strings.Contains(text, "[Resource: application/json]") {
		t.Errorf("expected resource placeholder, got: %s", text)
	}
	if !strings.Contains(text, "some text") {
		t.Errorf("expected unknown text, got: %s", text)
	}
	if !strings.Contains(text, "[Data: unknown]") {
		t.Errorf("expected data placeholder, got: %s", text)
	}
	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
}

func TestRedactImageData_Immutable(t *testing.T) {
	original := []mcp.ContentItem{
		{Type: "text", Text: "hello"},
		{Type: "image", MimeType: "image/png", Data: "base64data"},
	}
	originalData := original[1].Data

	redacted := redactImageData(original)

	// Original should not be modified
	if original[1].Data != originalData {
		t.Error("redactImageData mutated the original slice")
	}
	// Redacted should have modified data
	if !strings.Contains(redacted[1].Data, "[redacted") {
		t.Errorf("expected redacted data, got: %s", redacted[1].Data)
	}
	// Text items should be unchanged
	if redacted[0].Text != "hello" {
		t.Errorf("expected text unchanged, got: %s", redacted[0].Text)
	}
}

func TestRedactImageData_EmptyData(t *testing.T) {
	original := []mcp.ContentItem{
		{Type: "image", MimeType: "image/png", Data: ""},
	}
	redacted := redactImageData(original)

	if redacted[0].Data != "" {
		t.Errorf("expected empty data to stay empty, got: %s", redacted[0].Data)
	}
}
