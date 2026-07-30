package dingtalk

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
		"client_id":     " app-id ",
		"client_secret": " app-secret ",
		"operator_id":   " union-id ",
	}})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.ClientID != "app-id" || cfg.ClientSecret != "app-secret" || cfg.OperatorID != "union-id" {
		t.Fatalf("credentials not normalized: %#v", cfg)
	}
	if cfg.baseURL() != defaultAPIBaseURL {
		t.Fatalf("baseURL() = %q", cfg.baseURL())
	}

	tests := []struct {
		name string
		data map[string]interface{}
	}{
		{name: "missing client id", data: map[string]interface{}{"client_secret": "s", "operator_id": "o"}},
		{name: "missing client secret", data: map[string]interface{}{"client_id": "i", "operator_id": "o"}},
		{name: "missing operator", data: map[string]interface{}{"client_id": "i", "client_secret": "s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig(&types.DataSourceConfig{Credentials: tt.data})
			if !errors.Is(err, datasource.ErrInvalidCredentials) {
				t.Fatalf("parseConfig() error = %v", err)
			}
		})
	}

	_, err = parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
		"client_id": "i", "client_secret": "s", "operator_id": "o",
		"base_url": "http://169.254.169.254/latest/meta-data",
	}})
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("unsafe base URL error = %v", err)
	}
}

func TestResourceReferenceRoundTrip(t *testing.T) {
	root := resourceReference{WorkspaceID: "space / 1"}
	rootID, err := encodeResourceReference(root)
	if err != nil {
		t.Fatal(err)
	}
	folder := root.child("folder:1")
	folderID, err := encodeResourceReference(folder)
	if err != nil {
		t.Fatal(err)
	}
	doc := folder.child("doc?2")
	docID, err := encodeResourceReference(doc)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := decodeResourceReference(docID)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkspaceID != root.WorkspaceID || decoded.NodeID != "doc?2" ||
		len(decoded.Ancestors) != 1 || decoded.Ancestors[0] != "folder:1" {
		t.Fatalf("decoded reference = %#v", decoded)
	}

	ancestors, err := resourceAncestorIDs(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestors) != 2 || ancestors[0] != rootID || ancestors[1] != folderID {
		t.Fatalf("ancestor IDs = %#v, want [%q %q]", ancestors, rootID, folderID)
	}
}

func TestResourceReferenceRejectsInvalidInput(t *testing.T) {
	for _, raw := range []string{
		"",
		"other:v1?workspace=x",
		"dingtalk:v1?node=n",
		"dingtalk:v1?workspace=w&node=n&ancestor=n",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("decodeResourceReference(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestParseTimestampAndSanitizeFilename(t *testing.T) {
	want := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)
	if got := parseTimestamp("2026-07-24T01:02:03Z"); !got.Equal(want) {
		t.Fatalf("parseTimestamp() = %s", got)
	}
	if got := parseTimestamp("bad"); !got.IsZero() {
		t.Fatalf("invalid timestamp = %s", got)
	}

	if got := sanitizeFilename(` /bad:*?"<>|. `); got != "bad" {
		t.Fatalf("sanitizeFilename() = %q", got)
	}
	long := strings.Repeat("文", 100)
	got := sanitizeFilename(long)
	if len(got) > 200 || !strings.HasPrefix(long, got) {
		t.Fatalf("invalid UTF-8 truncation: bytes=%d value=%q", len(got), got)
	}
}
