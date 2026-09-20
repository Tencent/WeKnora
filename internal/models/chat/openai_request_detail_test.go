package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestConvertMessagesOmitsImplicitImageDetail is the chat-side regression
// test for issue #3451: the msg.Images fallback branch hardcoded
// image_url.detail="auto", which strict OpenAI-compatible endpoints (e.g.
// MiniMax) reject. detail is omitempty, so an unset value disappears from
// the request while providers default it to "auto" server-side. Explicitly
// configured detail in MultiContent parts must keep passing through.
func TestConvertMessagesOmitsImplicitImageDetail(t *testing.T) {
	c := &RemoteAPIChat{}

	converted := c.ConvertMessages([]Message{{
		Role:    "user",
		Content: "describe this",
		Images:  []string{"data:image/png;base64,aGVsbG8="},
	}})
	if len(converted) != 1 {
		t.Fatalf("converted %d messages, want 1", len(converted))
	}
	raw, err := json.Marshal(converted[0])
	if err != nil {
		t.Fatalf("marshal converted message: %v", err)
	}
	if strings.Contains(string(raw), `"detail"`) {
		t.Errorf("msg.Images fallback leaks detail: %s", raw)
	}

	explicit := c.ConvertMessages([]Message{{
		Role: "user",
		MultiContent: []MessageContentPart{{
			Type: "image_url",
			ImageURL: &ImageURL{
				URL:    "data:image/png;base64,aGVsbG8=",
				Detail: "low",
			},
		}},
	}})
	rawExplicit, err := json.Marshal(explicit[0])
	if err != nil {
		t.Fatalf("marshal explicit message: %v", err)
	}
	if !strings.Contains(string(rawExplicit), `"detail":"low"`) {
		t.Errorf("explicitly configured detail must pass through: %s", rawExplicit)
	}
}
