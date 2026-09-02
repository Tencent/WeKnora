package dingtalk

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestParseDownloadRewrite(t *testing.T) {
	cases := []struct {
		name       string
		cfg        *config.DingTalkURLRewriteConfig
		wantNil    bool
		wantPrefix []string
		wantTo     string
	}{
		{name: "nil config disables rewrite", cfg: nil, wantNil: true},
		{
			name: "commas and whitespace tolerated",
			cfg: &config.DingTalkURLRewriteConfig{
				From: " https://dingtalk-file.111.111.111.111:15443 , https://111.111.111.111:15443 ,, ",
				To:   "http://222.222.222.222:80",
			},
			wantPrefix: []string{"https://dingtalk-file.111.111.111.111:15443", "https://111.111.111.111:15443"},
			wantTo:     "http://222.222.222.222:80",
		},
		{
			name: "entry without scheme dropped",
			cfg: &config.DingTalkURLRewriteConfig{
				From: "dingtalk-file.111.111.111.111:15443,https://ok.example:15443",
				To:   "http://222.222.222.222:80",
			},
			wantPrefix: []string{"https://ok.example:15443"},
			wantTo:     "http://222.222.222.222:80",
		},
		{
			name: "empty to disables rewrite",
			cfg: &config.DingTalkURLRewriteConfig{
				From: "https://a.example:15443",
				To:   "  ",
			},
			wantNil: true,
		},
		{
			name: "to without scheme disables rewrite",
			cfg: &config.DingTalkURLRewriteConfig{
				From: "https://a.example:15443",
				To:   "222.222.222.222:80",
			},
			wantNil: true,
		},
		{
			name: "all prefixes invalid disables rewrite",
			cfg: &config.DingTalkURLRewriteConfig{
				From: "no-scheme.example",
				To:   "http://222.222.222.222:80",
			},
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDownloadRewrite(tc.cfg)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("parseDownloadRewrite = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("parseDownloadRewrite = nil, want rule")
			}
			if len(got.prefixes) != len(tc.wantPrefix) {
				t.Fatalf("prefixes = %v, want %v", got.prefixes, tc.wantPrefix)
			}
			for i := range tc.wantPrefix {
				if got.prefixes[i] != tc.wantPrefix[i] {
					t.Errorf("prefixes[%d] = %q, want %q", i, got.prefixes[i], tc.wantPrefix[i])
				}
			}
			if got.to != tc.wantTo {
				t.Errorf("to = %q, want %q", got.to, tc.wantTo)
			}
		})
	}
}

func TestApplyDownloadRewrite(t *testing.T) {
	r := parseDownloadRewrite(&config.DingTalkURLRewriteConfig{
		From: "https://dingtalk-file.111.111.111.111:15443,https://111.111.111.111:15443",
		To:   "http://222.222.222.222:80",
	})
	if r == nil {
		t.Fatal("parseDownloadRewrite returned nil for valid config")
	}

	cases := []struct {
		name        string
		rawURL      string
		want        string
		wantPrefix  string
		wantMatched bool
	}{
		{
			name:        "host prefix hit keeps path and query",
			rawURL:      "https://dingtalk-file.111.111.111.111:15443/temp/file?sig=abc",
			want:        "http://222.222.222.222:80/temp/file?sig=abc",
			wantPrefix:  "https://dingtalk-file.111.111.111.111:15443",
			wantMatched: true,
		},
		{
			name:        "second prefix hit",
			rawURL:      "https://111.111.111.111:15443/x/y.zip",
			want:        "http://222.222.222.222:80/x/y.zip",
			wantPrefix:  "https://111.111.111.111:15443",
			wantMatched: true,
		},
		{
			name:        "prefix equals full url",
			rawURL:      "https://111.111.111.111:15443",
			want:        "http://222.222.222.222:80",
			wantPrefix:  "https://111.111.111.111:15443",
			wantMatched: true,
		},
		{
			name:        "query directly after prefix matches",
			rawURL:      "https://111.111.111.111:15443?sig=1",
			want:        "http://222.222.222.222:80?sig=1",
			wantPrefix:  "https://111.111.111.111:15443",
			wantMatched: true,
		},
		{
			name:        "digit after prefix does not match (port overlap)",
			rawURL:      "https://111.111.111.111:154439/file",
			want:        "https://111.111.111.111:154439/file",
			wantMatched: false,
		},
		{
			name:        "different host does not match",
			rawURL:      "https://wukong-abc.oss-cn-hangzhou.aliyuncs.com/file?sig=x",
			want:        "https://wukong-abc.oss-cn-hangzhou.aliyuncs.com/file?sig=x",
			wantMatched: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, prefix, ok := r.apply(tc.rawURL)
			if ok != tc.wantMatched {
				t.Fatalf("apply(%q) matched = %v, want %v", tc.rawURL, ok, tc.wantMatched)
			}
			if got != tc.want {
				t.Errorf("apply(%q) = %q, want %q", tc.rawURL, got, tc.want)
			}
			if prefix != tc.wantPrefix {
				t.Errorf("matched prefix = %q, want %q", prefix, tc.wantPrefix)
			}
		})
	}
}

// TestApplyDownloadRewrite_PathPrefix verifies from/to may carry path prefixes.
func TestApplyDownloadRewrite_PathPrefix(t *testing.T) {
	r := parseDownloadRewrite(&config.DingTalkURLRewriteConfig{
		From: "https://files.example.com/ding",
		To:   "http://10.2.3.4:8080/mirror",
	})
	if r == nil {
		t.Fatal("parseDownloadRewrite returned nil for valid config")
	}
	got, _, ok := r.apply("https://files.example.com/ding/a/b.png")
	if !ok || got != "http://10.2.3.4:8080/mirror/a/b.png" {
		t.Fatalf("apply = (%q, %v), want (%q, true)", got, ok, "http://10.2.3.4:8080/mirror/a/b.png")
	}
}

// TestNewSkipVerifySSRFSafeDownloadClient verifies the non-rewritten download
// client keeps every SSRF safeguard while relaxing TLS verification.
func TestNewSkipVerifySSRFSafeDownloadClient(t *testing.T) {
	c := newSkipVerifySSRFSafeDownloadClient()
	rt, ok := c.Transport.(*secutils.SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("Transport = %T, want *secutils.SSRFValidatingRoundTripper", c.Transport)
	}
	tr, ok := rt.Base.(*http.Transport)
	if !ok {
		t.Fatalf("Base transport = %T, want *http.Transport", rt.Base)
	}
	if tr.DialContext == nil {
		t.Error("DialContext = nil, want SSRF-safe dial context")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("TLSClientConfig.InsecureSkipVerify = false, want true")
	}
}
