package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, read-only validation of existing synthetic cards. This never starts a
// channel, sends a message, follows a link, or downloads a card resource.
func TestCardReadLive(t *testing.T) {
	config := os.Getenv("WEKNORA_FEISHU_CARD_LIVE_CREDENTIALS_JSON")
	if config == "" {
		t.Skip("set live card credentials and existing message cases to validate Feishu reads")
	}
	var credentials struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
		BaseURL   string `json:"api_base_url"`
	}
	if json.Unmarshal([]byte(config), &credentials) != nil || credentials.AppID == "" || credentials.AppSecret == "" {
		t.Fatal("invalid live credentials")
	}
	var cases []struct {
		MessageID string `json:"message_id"`
		Contains  string `json:"contains"`
	}
	if json.Unmarshal([]byte(os.Getenv("WEKNORA_FEISHU_CARD_LIVE_CASES_JSON")), &cases) != nil || len(cases) == 0 {
		t.Fatal("supply explicit existing card cases")
	}
	a, err := NewAdapter(RegionFeishu, credentials.AppID, credentials.AppSecret, "", "", credentials.BaseURL)
	if err != nil {
		t.Fatal("invalid live adapter configuration")
	}
	for _, tc := range cases {
		t.Run(tc.MessageID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			if !feishuSafePathParam(tc.MessageID) {
				t.Fatal("invalid live message ID")
			}
			if dir := os.Getenv("WEKNORA_FEISHU_CARD_LIVE_OUTPUT_DIR"); dir != "" {
				var snapshot json.RawMessage
				err := a.getAPIJSON(ctx, "/open-apis/im/v1/messages/"+tc.MessageID+
					"?user_id_type=open_id&card_msg_content_type=user_card_content", &snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tc.MessageID+".json"), snapshot, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			items, err := a.ReadMessage(ctx, tc.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.MessageID != tc.MessageID {
					continue
				}
				var body strings.Builder
				for _, part := range item.Parts {
					body.WriteString(part.Text)
					if part.Type != "" {
						t.Fatal("card emitted downloadable resource")
					}
				}
				t.Logf("type=%s status=%s bytes=%d missing=%v", item.Type, item.CardStatus, body.Len(), item.Warnings)
				if item.Type != "interactive" || item.CardStatus != "complete" || tc.Contains == "" ||
					!strings.Contains(body.String(), tc.Contains) {
					t.Fatal("final card was not extracted completely or expected original text is missing")
				}
				return
			}
			t.Fatal("requested card missing")
		})
	}
}
