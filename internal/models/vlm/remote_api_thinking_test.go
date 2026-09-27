package vlm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TestPredictForwardsThinkingSwitch pins the contract of the per-call thinking
// switch: it must reach the wire as the vendor's own key, and an absent
// preference must leave the model to its own defaults.
//
// The construction below deliberately mirrors the production row for
// qwen3.8-27b-4bpw (provider "generic" with extra_config.thinking_control =
// "enable_thinking"). That extra_config is what selects the bare
// enable_thinking body key; a deployment using the "openai" interface type
// instead maps the same switch onto reasoning_effort, so this test would not
// see enable_thinking there.
func TestPredictForwardsThinkingSwitch(t *testing.T) {
	// httptest binds to 127.0.0.1, which the SSRF guard rejects by default.
	// The whitelist is process-global; only touch it when it isn't already
	// loaded, since this package's other tests don't depend on its contents.
	if !secutils.IsSSRFWhitelisted("127.0.0.1") {
		secutils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	}

	// 1x1 transparent PNG, so the probe body also carries a real image part.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
		0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 10, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00,
		0x00, 0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4,
		0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}

	for _, tc := range []struct {
		name     string
		call     func(v *RemoteAPIVLM) error
		wantOn   bool
		wantNone bool
	}{
		{
			name: "thinking off",
			call: func(v *RemoteAPIVLM) error {
				opts := &PredictOptions{Thinking: &falseVal}
				_, err := v.PredictWithOptions(context.Background(), [][]byte{png}, "p", opts)
				return err
			},
			wantOn: false,
		},
		{
			name: "thinking on",
			call: func(v *RemoteAPIVLM) error {
				opts := &PredictOptions{Thinking: &trueVal}
				_, err := v.PredictWithOptions(context.Background(), [][]byte{png}, "p", opts)
				return err
			},
			wantOn: true,
		},
		{
			name: "no preference expressed",
			call: func(v *RemoteAPIVLM) error {
				_, err := v.PredictWithOptions(context.Background(), [][]byte{png}, "p", &PredictOptions{})
				return err
			},
			wantNone: true,
		},
		{
			name: "plain Predict",
			call: func(v *RemoteAPIVLM) error {
				_, err := v.Predict(context.Background(), [][]byte{png}, "p")
				return err
			},
			wantNone: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer srv.Close()

			v, err := NewRemoteAPIVLM(&Config{
				BaseURL:   srv.URL,
				ModelName: "probe",
				APIKey:    "k",
				Provider:  "generic",
				Extra:     map[string]any{"thinking_control": "enable_thinking"},
			})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			if err := tc.call(v); err != nil {
				t.Fatalf("call: %v", err)
			}
			t.Logf("body: %s", body)

			// The switch must land as the vendor's own key. Assert on both
			// spellings so a change that relocates the field (say, into
			// chat_template_kwargs) is caught rather than silently passing.
			off := strings.Contains(body, `"enable_thinking":false`)
			on := strings.Contains(body, `"enable_thinking":true`)
			anyThinking := strings.Contains(body, "enable_thinking") ||
				strings.Contains(body, "chat_template_kwargs")
			if tc.wantOn && !on {
				t.Errorf("thinking=true did not reach the body: %s", body)
			}
			if !tc.wantOn && !tc.wantNone && !off {
				t.Errorf("thinking=false did not reach the body: %s", body)
			}
			if tc.wantNone && anyThinking {
				t.Errorf("expected no thinking field, got: %s", body)
			}
		})
	}
}

var (
	falseVal = false
	trueVal  = true
)
