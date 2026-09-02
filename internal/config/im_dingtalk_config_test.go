package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestIMConfigDingTalkYAMLParsing verifies the yaml keys of the optional
// im.dingtalk section (dedicated DingTalk intranet adaptation) round-trip.
func TestIMConfigDingTalkYAMLParsing(t *testing.T) {
	src := `
workers: 3
dingtalk:
  download_url_rewrite:
    from: "https://dingtalk-file.111.111.111.111:15443, https://111.111.111.111:15443"
    to: "http://222.222.222.222:80"
  download_insecure_skip_verify: true
`
	var cfg IMConfig
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal im config: %v", err)
	}
	if cfg.DingTalk == nil {
		t.Fatal("cfg.DingTalk = nil, want parsed section")
	}
	if cfg.DingTalk.DownloadURLRewrite == nil {
		t.Fatal("cfg.DingTalk.DownloadURLRewrite = nil, want parsed section")
	}
	wantFrom := "https://dingtalk-file.111.111.111.111:15443, https://111.111.111.111:15443"
	if got := cfg.DingTalk.DownloadURLRewrite.From; got != wantFrom {
		t.Errorf("From = %q, want %q", got, wantFrom)
	}
	if got := cfg.DingTalk.DownloadURLRewrite.To; got != "http://222.222.222.222:80" {
		t.Errorf("To = %q, want %q", got, "http://222.222.222.222:80")
	}
	if !cfg.DingTalk.DownloadInsecureSkipVerify {
		t.Error("DownloadInsecureSkipVerify = false, want true")
	}
}

// TestIMConfigDingTalkAbsent ensures a config without the dingtalk section
// leaves the pointer nil (feature disabled, zero behavior change).
func TestIMConfigDingTalkAbsent(t *testing.T) {
	var cfg IMConfig
	if err := yaml.Unmarshal([]byte("workers: 3"), &cfg); err != nil {
		t.Fatalf("unmarshal im config: %v", err)
	}
	if cfg.DingTalk != nil {
		t.Errorf("DingTalk = %+v, want nil when section absent", cfg.DingTalk)
	}
}
