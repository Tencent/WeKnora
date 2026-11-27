package vlm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// tallImagePNG encodes a 8×2000 PNG: a 250:1 strip, over the preprocessor
// limit that vLLM enforces ("absolute aspect ratio must be smaller than 200").
func tallImagePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 2000))
	for y := 0; y < 2000; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 30), uint8(y % 256), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode tall PNG: %v", err)
	}
	return buf.Bytes()
}

// imagePartOf extracts the image_url part from the last recorded request, or
// fails if there is none.
func imagePartOf(t *testing.T, lastRequest map[string]interface{}) map[string]interface{} {
	t.Helper()
	msgs, ok := lastRequest["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		t.Fatalf("request has no messages: %v", lastRequest)
	}
	msg, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first message is not an object: %T", msgs[0])
	}
	parts, ok := msg["content"].([]interface{})
	if !ok {
		t.Fatalf("message content is not a list: %v", msg)
	}
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if part["type"] == "image_url" {
			return part
		}
	}
	t.Fatalf("no image_url part in request: %v", msg)
	return nil
}

// TestRemoteAPIVLMClampsExtremeAspectRatio is the regression test for #5530:
// an image whose aspect ratio exceeds the vision preprocessor's limit is
// downscaled before it is sent, so it no longer fails with "absolute aspect
// ratio must be smaller than 200" on every retry.
func TestRemoteAPIVLMClampsExtremeAspectRatio(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "qwen-vl",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}

	if _, err := v.Predict(t.Context(), [][]byte{tallImagePNG(t)}, "describe this"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	part := imagePartOf(t, lastRequest)
	imgURL, ok := part["image_url"].(map[string]interface{})
	if !ok {
		t.Fatalf("image_url is not an object: %v", part)
	}
	url, _ := imgURL["url"].(string)
	// Decode the data URI and measure the sent image's aspect ratio.
	b64 := url[strings.Index(url, ",")+1:]
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode data URI: %v", err)
	}
	sent, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode sent image: %v", err)
	}
	b := sent.Bounds()
	w, h := b.Dx(), b.Dy()
	ratio := float64(h) / float64(w)
	if ratio >= 200 {
		t.Errorf("sent image still has aspect ratio %f (≥200): the preprocessor would reject it", ratio)
	}
}

// TestRemoteAPIVLMSkipsDegenerateStrip is the second half of #5530: a 1-pixel
// rule extracted as its own resource carries no content, so it is dropped
// before the request rather than sent to the model.
func TestRemoteAPIVLMSkipsDegenerateStrip(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "qwen-vl",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}

	strip := image.NewRGBA(image.Rect(0, 0, 1, 526))
	var buf bytes.Buffer
	if err := png.Encode(&buf, strip); err != nil {
		t.Fatalf("encode strip: %v", err)
	}

	if _, err := v.Predict(t.Context(), [][]byte{buf.Bytes()}, "describe this"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	// The degenerate image must not appear in the request: the only content
	// part left is the text prompt.
	msgs := lastRequest["messages"].([]interface{})
	msg := msgs[0].(map[string]interface{})
	parts := msg["content"].([]interface{})
	for _, p := range parts {
		if part, ok := p.(map[string]interface{}); ok && part["type"] == "image_url" {
			t.Errorf("degenerate strip was sent to the model: %v", part)
		}
	}

	// Sanity: the request still decodes as JSON.
	if _, err := json.Marshal(lastRequest); err != nil {
		t.Errorf("marshal request: %v", err)
	}
}
