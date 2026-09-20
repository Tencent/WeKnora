package vlm

import (
	"testing"
)

// TestRemoteAPIVLMOmitsImageDetail is the regression test for issue #3451:
// every OCR/Caption request sent image_url.detail="auto". OpenAI defaults
// the field to "auto" server-side, but strict OpenAI-compatible endpoints
// (e.g. MiniMax, error "invalid image detail: auto") reject values outside
// their whitelist, so the hardcoded value broke all image parsing there.
// The field is omitempty in go-openai, so leaving it unset removes it from
// the wire request entirely.
func TestRemoteAPIVLMOmitsImageDetail(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "gpt-4o",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}

	if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract the text"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	messages, _ := lastRequest["messages"].([]interface{})
	if len(messages) == 0 {
		t.Fatal("request carries no messages")
	}
	first, _ := messages[0].(map[string]interface{})
	content, _ := first["content"].([]interface{})
	foundImage := false
	for _, part := range content {
		partMap, ok := part.(map[string]interface{})
		if !ok || partMap["type"] != "image_url" {
			continue
		}
		foundImage = true
		imageURL, _ := partMap["image_url"].(map[string]interface{})
		if _, ok := imageURL["detail"]; ok {
			t.Errorf("image_url carries detail=%v, which strict OpenAI-compatible endpoints reject (#3451)", imageURL["detail"])
		}
	}
	if !foundImage {
		t.Error("request carries no image part")
	}
}
