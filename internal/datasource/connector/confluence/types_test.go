package confluence

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSafeFilenameKeepsUTF8ValidAtLimit(t *testing.T) {
	name := strings.Repeat("知识库", 150)
	got := safeFilename(name)
	if !utf8.ValidString(got) || len([]rune(got)) != 200 {
		t.Fatalf("safeFilename() truncated UTF-8 incorrectly: %q", got)
	}
}

func TestDecodeCursorCloneDoesNotMutateBaseline(t *testing.T) {
	old := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"space_pages": map[string]interface{}{"space": map[string]interface{}{"page": "v:2"}},
	}}
	baseline := decodeCursor(old)
	next := baseline.clone()
	next.SpacePages["space"]["page"] = "v:3"
	if baseline.SpacePages["space"]["page"] != "v:2" {
		t.Fatal("cursor clone mutated previous checkpoint")
	}
}

func TestParseConfigUsesCloudToken(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
		"edition":   "cloud",
		"base_url":  "https://team.atlassian.net/wiki",
		"username":  "user@example.com",
		"api_token": "token",
	}})
	if err != nil || !cfg.cloud() || cfg.secret != "token" {
		t.Fatalf("parseConfig() = %#v, %v", cfg, err)
	}
}

func TestParseConfigReadsPublicFieldsFromSettings(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{"api_token": "token"},
		Settings: map[string]interface{}{
			"edition": "cloud", "base_url": "https://team.atlassian.net/wiki", "username": "user@example.com",
		},
	})
	if err != nil || !cfg.cloud() || cfg.username != "user@example.com" || cfg.secret != "token" {
		t.Fatalf("parseConfig() = %#v, %v", cfg, err)
	}
}

func TestParseConfigNormalizesSchemeAndCloudWikiPath(t *testing.T) {
	server, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
		"base_url": "confluence.example.com:8090", "username": "reader", "password": "secret",
	}})
	if err != nil || server.baseURL != "https://confluence.example.com:8090" {
		t.Fatalf("server parseConfig() = %#v, %v", server, err)
	}

	cloud, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{
		"edition": "cloud", "base_url": "https://team.atlassian.net",
		"username": "user@example.com", "api_token": "token",
	}})
	if err != nil || cloud.baseURL != "https://team.atlassian.net/wiki" {
		t.Fatalf("cloud parseConfig() = %#v, %v", cloud, err)
	}
}

func TestPageFileNameIncludesID(t *testing.T) {
	if got := pageFileName("Overview", "98308"); got != "Overview-98308.md" {
		t.Fatalf("pageFileName() = %q", got)
	}
}

func TestPageVersionUnknown(t *testing.T) {
	withNumber := page{}
	withNumber.Version.Number = 7
	withWhen := page{}
	withWhen.Version.When = "2026-01-02T03:04:05Z"
	withCreatedAt := page{}
	withCreatedAt.Version.CreatedAt = "2026-01-02T03:04:05Z"

	for _, tc := range []struct {
		name      string
		in        page
		want      string
		wantKnown bool
	}{
		{"number", withNumber, "v:7", true},
		{"when", withWhen, "t:2026-01-02T03:04:05Z", true},
		{"createdAt", withCreatedAt, "t:2026-01-02T03:04:05Z", true},
		{"unknown", page{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := pageVersion(tc.in)
			if got != tc.want || known != tc.wantKnown {
				t.Fatalf("pageVersion() = (%q, %v); want (%q, %v)", got, known, tc.want, tc.wantKnown)
			}
		})
	}
}

func TestPrepareSyncCursorsPreservesFullSyncBaseline(t *testing.T) {
	old := streamCursor(map[string]string{"p1": "v:1", "p2": "v:1"})
	baseline, next := prepareSyncCursors(old, true)
	if len(next.SpacePages) != 0 || baseline.SpacePages["1"]["p1"] != "v:1" || !next.FullSync {
		t.Fatalf("first full sync baseline=%#v next=%#v", baseline, next)
	}
	next.SpacePages["1"] = map[string]string{"p1": "v:1"}
	resumeBaseline, resumeNext := prepareSyncCursors(next.syncCursor(), true)
	if resumeNext.SpacePages["1"]["p1"] != "v:1" || resumeBaseline.SpacePages["1"]["p2"] != "v:1" {
		t.Fatalf("resume baseline=%#v next=%#v", resumeBaseline, resumeNext)
	}
}
