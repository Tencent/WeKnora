package lingo

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRenderEntityRichTextAndAliases(t *testing.T) {
	entity := Entity{
		ID: "enterprise_1",
		MainKeys: []MainKey{
			{Key: "OT"},
			{Key: "Overtime"},
		},
		Aliases:    []MainKey{{Key: "加班"}},
		RichText:   "<p>Work beyond regular hours</p>",
		CreateTime: "1700000000",
		UpdateTime: "1700000100",
	}
	item, err := renderEntity(entity, "repo-1", settings{FolderPath: "飞书词典"}, map[string]int{"OT": 1})
	if err != nil {
		t.Fatalf("renderEntity() error = %v", err)
	}
	if item.ExternalID != "enterprise_1" {
		t.Fatalf("ExternalID = %q", item.ExternalID)
	}
	if item.FileName != "飞书词典/OT.md" {
		t.Fatalf("FileName = %q", item.FileName)
	}
	body := string(item.Content)
	for _, want := range []string{"# OT", "## 别名", "Overtime、加班", "Work beyond regular hours", "飞书词条 ID：enterprise_1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("content missing %q: %s", want, body)
		}
	}
	if item.SourceResourceID != "repo-1" {
		t.Fatalf("SourceResourceID = %q", item.SourceResourceID)
	}
	if got := item.UpdatedAt.Unix(); got != 1700000100 {
		t.Fatalf("UpdatedAt = %d", got)
	}
	if item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be parsed")
	}
	if item.Metadata["feishu_entity_id"] != "enterprise_1" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
}

func TestRenderEntityFallsBackToDescription(t *testing.T) {
	entity := Entity{
		ID:          "enterprise_2",
		MainKeys:    []MainKey{{Key: "IMG"}},
		RichText:    `<p><img src="https://example.com/a.png"/></p>`,
		Description: "Plain definition",
	}
	item, err := renderEntity(entity, defaultRepoID, settings{FolderPath: "飞书词典"}, map[string]int{"IMG": 1})
	if err != nil {
		t.Fatalf("renderEntity() error = %v", err)
	}
	if !strings.Contains(string(item.Content), "Plain definition") {
		t.Fatalf("content = %s", item.Content)
	}
}

func TestRenderEntitySkipsUnsupportedRichTextWithoutDescription(t *testing.T) {
	entity := Entity{
		ID:       "enterprise_3",
		MainKeys: []MainKey{{Key: "IMG"}},
		RichText: `<p><img src="https://example.com/a.png"/></p>`,
	}
	_, err := renderEntity(entity, defaultRepoID, settings{FolderPath: "飞书词典"}, map[string]int{"IMG": 1})
	if !errors.Is(err, errUnsupportedRichText) {
		t.Fatalf("error = %v, want errUnsupportedRichText", err)
	}
}

func TestRenderEntityRejectsEmptyDefinition(t *testing.T) {
	entity := Entity{ID: "enterprise_4", MainKeys: []MainKey{{Key: "EMPTY"}}}
	_, err := renderEntity(entity, defaultRepoID, settings{FolderPath: "飞书词典"}, map[string]int{"EMPTY": 1})
	if !errors.Is(err, errEmptyDefinition) {
		t.Fatalf("error = %v, want errEmptyDefinition", err)
	}
}

func TestRenderEntityAddsSuffixForDuplicateTitles(t *testing.T) {
	entity := Entity{
		ID:          "enterprise_test_0001",
		MainKeys:    []MainKey{{Key: "AS"}},
		Description: "first AS",
	}
	item, err := renderEntity(entity, "repo-1", settings{FolderPath: "飞书词典"}, map[string]int{"AS": 2})
	if err != nil {
		t.Fatalf("renderEntity() error = %v", err)
	}
	if item.FileName != "飞书词典/AS (test0001).md" {
		t.Fatalf("FileName = %q", item.FileName)
	}
}

func TestUnixSeconds(t *testing.T) {
	if got := unixSeconds("1700000000"); got.UTC() != time.Unix(1700000000, 0).UTC() {
		t.Fatalf("unixSeconds = %v", got)
	}
	if got := unixSeconds(""); !got.IsZero() {
		t.Fatalf("empty unixSeconds = %v", got)
	}
	if got := unixSeconds("not-a-number"); !got.IsZero() {
		t.Fatalf("invalid unixSeconds = %v", got)
	}
}
