package feishu

import (
	"errors"
	"strings"
	"testing"
)

// feishuFailure classifies a raw connector/API error into a stable i18n code
// (+ optional numeric feishu code) and an English fallback message. The frontend
// localises by code; the raw status/body/log_id stays in the server logs and
// must never appear in the code, codeValue or fallback.
func TestFeishuFailure(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantCode      string
		wantCodeValue string
		fallbackHas   []string
		noLeak        []string // must appear in neither code nor fallback
	}{
		{
			name:        "rate limited",
			err:         errors.New("feishu rate limited: status=429 body={\"code\":99991400,\"msg\":\"too many request\"}"),
			wantCode:    "feishu_rate_limited",
			fallbackHas: []string{"retry"},
			noLeak:      []string{"body=", "{", "99991400"},
		},
		{
			name:     "server 5xx",
			err:      errors.New("feishu server error: status=500 body={\"code\":1663}"),
			wantCode: "feishu_server_unavailable",
			noLeak:   []string{"body=", "{"},
		},
		{
			name:     "export timeout",
			err:      errors.New("export 季度报告 (docx): export task timed out after 60s (ticket=abc)"),
			wantCode: "feishu_timeout",
			noLeak:   []string{"ticket=", "abc"},
		},
		{
			name:          "api error carries the feishu code as a param",
			err:           errors.New("feishu api error: status=500 body={\"code\":1663,\"msg\":\"internal error\",\"error\":{\"log_id\":\"20260\"}}"),
			wantCode:      "feishu_api_error",
			wantCodeValue: "1663",
			noLeak:        []string{"log_id", "body=", "{"},
		},
		{
			name:     "api error without a code is generic",
			err:      errors.New("feishu api error: status=502 body=bad gateway"),
			wantCode: "feishu_api_error_generic",
		},
		{
			name:     "auth failure is actionable, not a retry",
			err:      errors.New("feishu auth error: code=99991663 msg=Invalid access token"),
			wantCode: "feishu_auth_or_permission",
			noLeak:   []string{"Invalid access token", "99991663"},
		},
		{
			name:     "unknown error falls back to sync_failed",
			err:      errors.New("some totally unexpected failure"),
			wantCode: "sync_failed",
			noLeak:   []string{"totally unexpected"},
		},
		{
			// "decode"/"encode"/"unicode" all contain the substring "code" but
			// are not Feishu API errors — they must not be classified as one.
			name:     "decode error is not a feishu api error",
			err:      errors.New("failed to decode response body"),
			wantCode: "sync_failed",
		},
		{
			// The failed item is retried on the next sync (the cursor is not
			// advanced), which for a manual-only source is not automatic. The
			// message must not promise an unconditional automatic retry.
			name:        "transient message promises retry on next sync, not automatic",
			err:         errors.New("feishu rate limited: status=429"),
			wantCode:    "feishu_rate_limited",
			fallbackHas: []string{"next sync"},
			noLeak:      []string{"automatically"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, codeValue, fallback := feishuFailure(tc.err)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if codeValue != tc.wantCodeValue {
				t.Errorf("codeValue = %q, want %q", codeValue, tc.wantCodeValue)
			}
			if strings.TrimSpace(fallback) == "" {
				t.Errorf("fallback message must not be empty")
			}
			for _, s := range tc.fallbackHas {
				if !strings.Contains(strings.ToLower(fallback), s) {
					t.Errorf("fallback %q missing %q", fallback, s)
				}
			}
			for _, s := range tc.noLeak {
				if strings.Contains(code, s) || strings.Contains(fallback, s) {
					t.Errorf("raw detail %q leaked into code/fallback (code=%q fallback=%q)", s, code, fallback)
				}
			}
		})
	}
}
