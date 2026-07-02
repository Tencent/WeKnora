package asr

import (
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func withASRSSRFWhitelist(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", raw)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestOpenAIASRRejectsInternalBaseURL(t *testing.T) {
	withASRSSRFWhitelist(t, "")

	_, err := NewOpenAIASR(&Config{
		BaseURL:   "http://169.254.169.254/latest/meta-data/",
		ModelName: "asr-test",
	})
	if err == nil {
		t.Fatalf("NewOpenAIASR returned nil error for blocked internal BaseURL")
	}
}
