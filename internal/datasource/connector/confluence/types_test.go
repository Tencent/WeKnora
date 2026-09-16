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
		"edition": "cloud", "base_url": "https://team.atlassian.net/wiki", "username": "user@example.com", "api_token": "token",
	}})
	if err != nil || !cfg.cloud() || cfg.secret != "token" {
		t.Fatalf("parseConfig() = %#v, %v", cfg, err)
	}
}
