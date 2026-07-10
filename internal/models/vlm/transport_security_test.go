package vlm

import (
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func withVLMSSRFWhitelist(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", raw)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestRemoteAPIVLMRejectsInternalBaseURL(t *testing.T) {
	withVLMSSRFWhitelist(t, "")

	_, err := NewRemoteAPIVLM(&Config{
		BaseURL:   "http://169.254.169.254/latest/meta-data/",
		ModelName: "vlm-test",
	})
	if err == nil {
		t.Fatalf("NewRemoteAPIVLM returned nil error for blocked internal BaseURL")
	}
}
