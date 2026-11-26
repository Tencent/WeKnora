package vlm

import "testing"

// TestRemoteAPIVLMDefaultTemperatureIsExactLiteral is the regression test for
// the float32 default: float64(float32(0.1)) serialises as
// 0.10000000149011612, which providers that validate the decimal places of
// temperature (e.g. Zhipu GLM, error 1210) reject. The default must reach the
// wire as exactly 0.1.
func TestRemoteAPIVLMDefaultTemperatureIsExactLiteral(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "glm-4.6v-flash",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}
	if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract the text"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	got, ok := lastRequest["temperature"]
	if !ok {
		t.Fatal("request is missing temperature")
	}
	if got != float64(0.1) {
		t.Errorf("temperature = %v, want exactly 0.1", got)
	}
}
