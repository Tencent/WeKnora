package config

import (
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// TestIMConfigDingTalkYAMLParsing verifies the optional im.dingtalk section
// (dedicated DingTalk intranet adaptation) parses the yaml keys.
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

// TestIMConfigDingTalkViperLoadPath verifies the production config loading
// path (viper → mapstructure with yaml tags) parses im.dingtalk the same
// way as the direct yaml.Unmarshal path, guarding against silent
// mapstructure key mismatches.
func TestIMConfigDingTalkViperLoadPath(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetConfigType("yaml")
	src := `
im:
  workers: 3
  dingtalk:
    download_url_rewrite:
      from: "https://dingtalk-file.111.111.111.111:15443,https://111.111.111.111:15443"
      to: "http://222.222.222.222:80"
    download_insecure_skip_verify: true
`
	if err := viper.ReadConfig(strings.NewReader(src)); err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "yaml"
	}); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.IM == nil || cfg.IM.DingTalk == nil {
		t.Fatal("cfg.IM.DingTalk = nil, want parsed section via viper path")
	}
	dt := cfg.IM.DingTalk
	if dt.DownloadURLRewrite == nil {
		t.Fatal("DownloadURLRewrite = nil, want parsed section via viper path")
	}
	if want := "https://dingtalk-file.111.111.111.111:15443,https://111.111.111.111:15443"; dt.DownloadURLRewrite.From != want {
		t.Errorf("From = %q, want %q", dt.DownloadURLRewrite.From, want)
	}
	if dt.DownloadURLRewrite.To != "http://222.222.222.222:80" {
		t.Errorf("To = %q, want %q", dt.DownloadURLRewrite.To, "http://222.222.222.222:80")
	}
	if !dt.DownloadInsecureSkipVerify {
		t.Error("DownloadInsecureSkipVerify = false, want true")
	}
}

// TestIMConfigDingTalkEmptySection ensures an empty `dingtalk:` section
// parses to a non-nil DingTalk with nil DownloadURLRewrite (rewrite
// disabled downstream, no panic).
func TestIMConfigDingTalkEmptySection(t *testing.T) {
	var cfg IMConfig
	if err := yaml.Unmarshal([]byte("dingtalk: {}\n"), &cfg); err != nil {
		t.Fatalf("unmarshal im config: %v", err)
	}
	if cfg.DingTalk == nil {
		t.Fatal("DingTalk = nil for empty section, want non-nil empty struct")
	}
	if cfg.DingTalk.DownloadURLRewrite != nil {
		t.Errorf("DownloadURLRewrite = %+v, want nil for empty section", cfg.DingTalk.DownloadURLRewrite)
	}
}
