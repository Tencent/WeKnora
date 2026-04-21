package yuque

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// Yuque's runtime API returns the doc `status` field as an integer (0 or 1),
// even though its OpenAPI spec declares it as string. We tolerate both forms.
func TestV2Doc_Status_AcceptsNumberAndString(t *testing.T) {
	cases := []struct {
		name string
		body string
		want flexibleStatus
	}{
		{"number 1", `{"id":1,"status":1}`, "1"},
		{"number 0", `{"id":1,"status":0}`, "0"},
		{"string 1", `{"id":1,"status":"1"}`, "1"},
		{"null", `{"id":1,"status":null}`, ""},
		{"missing", `{"id":1}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d v2Doc
			if err := json.Unmarshal([]byte(tc.body), &d); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if d.Status != tc.want {
				t.Errorf("Status = %q, want %q", d.Status, tc.want)
			}
		})
	}
}

func TestV2DocDetail_Status_AcceptsNumber(t *testing.T) {
	var d v2DocDetail
	if err := json.Unmarshal([]byte(`{"id":1,"status":1}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Status != "1" {
		t.Errorf("Status = %q, want %q", d.Status, "1")
	}
}

func TestV2DocListResponse_Status_Number(t *testing.T) {
	// Reproduces the real-world failure:
	//   decode response: json: cannot unmarshal number into Go struct field v2Doc.data.status of type string
	body := `{"meta":{"total":1},"data":[{"id":101,"type":"Doc","status":1,"title":"hello"}]}`
	var resp v2DocListResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(Data) = %d", len(resp.Data))
	}
	if resp.Data[0].Status != "1" {
		t.Errorf("Status = %q, want %q", resp.Data[0].Status, "1")
	}
}

// flexibleStatus deliberately rejects unexpected JSON shapes so a future Yuque
// schema drift (e.g. status becoming a float, bool, array) fails loudly rather
// than being silently stringified and bypassing the `!= "1"` filter.
func TestV2Doc_Status_RejectsUnexpectedShapes(t *testing.T) {
	cases := []string{
		`{"id":1,"status":true}`,
		`{"id":1,"status":1.5}`,
		`{"id":1,"status":[1]}`,
		`{"id":1,"status":{"x":1}}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			var d v2Doc
			if err := json.Unmarshal([]byte(body), &d); err == nil {
				t.Errorf("expected error for %s, got Status=%q", body, d.Status)
			}
		})
	}
}

func TestParseYuqueConfig(t *testing.T) {
	t.Run("valid full", func(t *testing.T) {
		cfg, err := parseYuqueConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"api_token": "tok-123",
				"base_url":  "https://company.yuque.com",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.APIToken != "tok-123" {
			t.Errorf("APIToken = %q, want tok-123", cfg.APIToken)
		}
		if cfg.BaseURL != "https://company.yuque.com" {
			t.Errorf("BaseURL = %q", cfg.BaseURL)
		}
	})

	t.Run("default base_url", func(t *testing.T) {
		cfg, err := parseYuqueConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{"api_token": "tok-abc"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GetBaseURL() != "https://www.yuque.com" {
			t.Errorf("GetBaseURL default = %q, want https://www.yuque.com", cfg.GetBaseURL())
		}
	})

	t.Run("missing api_token", func(t *testing.T) {
		_, err := parseYuqueConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{},
		})
		if err == nil {
			t.Fatal("expected error for missing api_token")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseYuqueConfig(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})
}

func TestGetBaseURL_TrimsTrailingSlash(t *testing.T) {
	c := &Config{BaseURL: "https://x.yuque.com/"}
	if got := c.GetBaseURL(); got != "https://x.yuque.com" {
		t.Errorf("GetBaseURL = %q, want https://x.yuque.com", got)
	}
}

func TestGetBaseURL_AddsSchemeIfMissing(t *testing.T) {
	c := &Config{BaseURL: "company.yuque.com"}
	if got := c.GetBaseURL(); got != "https://company.yuque.com" {
		t.Errorf("GetBaseURL = %q, want https://company.yuque.com", got)
	}
}

func TestGetBaseURL_EmptyUsesDefault(t *testing.T) {
	c := &Config{BaseURL: ""}
	if got := c.GetBaseURL(); got != "https://www.yuque.com" {
		t.Errorf("GetBaseURL = %q, want default", got)
	}
}

func TestGetBaseURL_TrimsWhitespace(t *testing.T) {
	c := &Config{BaseURL: "  https://x.yuque.com/  "}
	if got := c.GetBaseURL(); got != "https://x.yuque.com" {
		t.Errorf("GetBaseURL = %q, want https://x.yuque.com", got)
	}
}

func TestSanitizeFileName_TruncatesAtRuneBoundary(t *testing.T) {
	// Long Chinese title (each 测 is 3 bytes in UTF-8). Raw byte slicing at 200
	// would split a rune and produce invalid UTF-8 that downstream filename
	// validation rejects with "文件名包含非法字符".
	long := strings.Repeat("测试", 100) // 600 bytes
	got := sanitizeFileName(long)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeFileName produced invalid UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Errorf("len = %d, want ≤ 200", len(got))
	}
	if len(got) == 0 {
		t.Error("result is empty")
	}
}
