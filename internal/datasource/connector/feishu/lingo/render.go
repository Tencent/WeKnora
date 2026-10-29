package lingo

import (
	"errors"
	"fmt"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	errEmptyDefinition     = errors.New("entity has no description or rich text")
	errUnsupportedRichText = errors.New("rich text contains unsupported media/table markup")
)

// unsupportedRichText reports markup that cannot be rendered safely into a
// self-contained Markdown document. The plain description is preferred for
// these entries; when it is absent the entity is skipped and logged.
func unsupportedRichText(html string) bool {
	lower := strings.ToLower(html)
	for _, tag := range []string{"<img", "<table", "<iframe", "<video", "<audio", "<svg"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	return false
}

func entityDefinition(e Entity) (string, error) {
	rich := strings.TrimSpace(e.RichText)
	description := strings.TrimSpace(e.Description)

	if rich != "" {
		if unsupportedRichText(rich) {
			if description != "" {
				return description, nil
			}
			return "", errUnsupportedRichText
		}
		markdown, err := htmltomd.ConvertString(rich)
		if err == nil && strings.TrimSpace(markdown) != "" {
			return strings.TrimSpace(markdown), nil
		}
	}
	if description != "" {
		return description, nil
	}
	return "", errEmptyDefinition
}

// shortEntityID returns a human-readable suffix used only when two entities in
// the same sync share a title (Feishu allows that), so their files do not
// collide.
func shortEntityID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if len(cleaned) > 8 {
		return cleaned[len(cleaned)-8:]
	}
	if cleaned == "" {
		return "entity"
	}
	return cleaned
}

func renderEntity(e Entity, repoID string, cfg settings, titleCounts map[string]int) (types.FetchedItem, error) {
	title := entityTitle(e)
	if title == "" {
		return types.FetchedItem{}, fmt.Errorf("entity %s has no term name", e.ID)
	}
	definition, err := entityDefinition(e)
	if err != nil {
		return types.FetchedItem{}, fmt.Errorf("entity %s (%s): %w", e.ID, title, err)
	}

	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", title)
	if aliases := entityAliases(e); len(aliases) > 0 {
		fmt.Fprintf(&body, "## 别名\n%s\n\n", strings.Join(aliases, "、"))
	}
	fmt.Fprintf(&body, "## 词条释义\n\n%s\n\n## 来源\n飞书词典\n\n飞书词条 ID：%s\n", definition, e.ID)

	name := datasource.SanitizeFileName(title)
	if titleCounts[title] > 1 {
		name = fmt.Sprintf("%s (%s)", name, shortEntityID(e.ID))
	}

	return types.FetchedItem{
		ExternalID:       e.ID,
		Title:            title,
		Content:          []byte(body.String()),
		ContentType:      "text/markdown",
		FileName:         buildFolderFileName(cfg.FolderPath, name+".md"),
		UpdatedAt:        unixSeconds(e.UpdateTime),
		CreatedAt:        unixSeconds(e.CreateTime),
		SourceResourceID: repoID,
		Metadata: map[string]string{
			"feishu_entity_id": e.ID,
			"repo_id":          repoID,
		},
	}, nil
}
