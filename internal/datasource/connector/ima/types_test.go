package ima

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestLogicalKey_IsStableAndScoped(t *testing.T) {
	base := logicalKey("kb1", "f1", "Doc")

	if base != logicalKey("kb1", "f1", "Doc") {
		t.Error("logicalKey must be deterministic")
	}
	if !strings.HasPrefix(base, "ima_") || len(base) != 36 {
		t.Errorf("key = %q, want an ima_ prefix and 36 chars so it fits varchar(64)", base)
	}

	for _, other := range []string{
		logicalKey("kb2", "f1", "Doc"),
		logicalKey("kb1", "f2", "Doc"),
		logicalKey("kb1", "f1", "Other"),
	} {
		if other == base {
			t.Errorf("keys must differ across kb/folder/title, both were %q", base)
		}
	}
}

// TestLogicalKey_DelimiterIsUnambiguous guards against a key collision from
// naively concatenating the components.
func TestLogicalKey_DelimiterIsUnambiguous(t *testing.T) {
	if logicalKey("kb", "a", "bc") == logicalKey("kb", "ab", "c") {
		t.Error("component boundaries must not be ambiguous")
	}
}

func TestParseIMAConfig(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]interface{}
		wantErr     error
		wantErrText string
	}{
		{
			name:        "valid",
			credentials: map[string]interface{}{"client_id": "cid", "api_key": "key"},
		},
		{
			name:        "missing client_id",
			credentials: map[string]interface{}{"api_key": "key"},
			wantErr:     datasource.ErrInvalidCredentials,
		},
		{
			name:        "blank api_key",
			credentials: map[string]interface{}{"client_id": "cid", "api_key": "   "},
			wantErr:     datasource.ErrInvalidCredentials,
		},
		{
			name:        "loopback base_url rejected by SSRF policy",
			credentials: map[string]interface{}{"client_id": "cid", "api_key": "key", "base_url": "http://169.254.169.254"},
			wantErrText: "SSRF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseIMAConfig(&types.DataSourceConfig{Credentials: tt.credentials})
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.wantErrText != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErrText)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestParseIMAConfig_NilConfig(t *testing.T) {
	if _, err := parseIMAConfig(nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestConfig_GetBaseURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", DefaultBaseURL},
		{"   ", DefaultBaseURL},
		{"https://ima.example.com/", "https://ima.example.com"},
		{"ima.example.com", "https://ima.example.com"},
		{"http://ima.example.com//", "http://ima.example.com"},
	}
	for _, tt := range tests {
		if got := (&Config{BaseURL: tt.in}).GetBaseURL(); got != tt.want {
			t.Errorf("GetBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtensionForContentType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"image/jpeg", "jpg"},
		{"image/JPEG", "jpg"},
		{"image/png; charset=binary", "png"},
		{"image/webp", "webp"},
		{"application/pdf", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := extensionForContentType(tt.in); got != tt.want {
			t.Errorf("extensionForContentType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName(""); got != "untitled" {
		t.Errorf("empty name = %q, want untitled", got)
	}
	if got := sanitizeFileName(`a/b\c:d*e?f"g<h>i|j`); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("filesystem-hostile characters survived: %q", got)
	}

	// A long CJK title must be truncated on a rune boundary, not mid-sequence.
	long := strings.Repeat("知识", 200)
	got := sanitizeFileName(long)
	if len(got) > 200 {
		t.Errorf("len = %d bytes, want <= 200", len(got))
	}
	if !strings.HasPrefix(long, got) {
		t.Errorf("truncation corrupted the prefix: %q", got)
	}
}

func TestIsSkippableMediaType(t *testing.T) {
	for _, mt := range []int32{mediaTypeNote, mediaTypeAISession, mediaTypeVideo} {
		if !isSkippableMediaType(mt) {
			t.Errorf("media type %d should be skippable", mt)
		}
	}
	for _, mt := range []int32{mediaTypePDF, mediaTypeMarkdown, mediaTypeWeb} {
		if isSkippableMediaType(mt) {
			t.Errorf("media type %d should not be skippable", mt)
		}
	}
}
