package knowledge

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CanonicalKnowledgeTitle removes storage/type decoration from a user-facing
// object name. Contextual or compound wording is left intact because deciding
// whether it represents one object or several requires semantic evidence.
func CanonicalKnowledgeTitle(title string) string {
	return stripIdentityTypeDecoration(strings.TrimSpace(title))
}

// RewriteWikiObjectIdentity preserves the generated object content while
// replacing model-owned identity fields with the server-resolved canonical
// identity. The canonical title is kept consistent across page metadata and
// the single H1 required by the Wiki object contract.
func RewriteWikiObjectIdentity(content, objectID, canonicalTitle string) (string, error) {
	objectID = strings.TrimSpace(objectID)
	canonicalTitle = CanonicalKnowledgeTitle(canonicalTitle)
	if objectID == "" || canonicalTitle == "" {
		return "", fmt.Errorf("canonical object ID and title are required")
	}
	frontmatter, body := parseWikiFrontmatter(content)
	if len(frontmatter) == 0 {
		return "", fmt.Errorf("knowledge object frontmatter is required")
	}
	frontmatter["knowledge_object_id"] = objectID
	if _, exists := frontmatter["id"]; exists {
		frontmatter["id"] = objectID
	}
	frontmatter["title"] = canonicalTitle
	if knowledgeType := KnowledgeType(strings.ToLower(strings.TrimSpace(stringValue(frontmatter["primary_type"])))); knowledgeType == TypeEntity {
		frontmatter["canonical_name"] = canonicalTitle
	}
	encoded, err := yaml.Marshal(frontmatter)
	if err != nil {
		return "", fmt.Errorf("marshal canonical knowledge object frontmatter: %w", err)
	}
	body, err = rewriteFirstHeading(body, canonicalTitle)
	if err != nil {
		return "", err
	}
	return "---\n" + strings.TrimSpace(string(encoded)) + "\n---\n\n" + strings.TrimSpace(body) + "\n", nil
}

func rewriteFirstHeading(body, title string) (string, error) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			lines[index] = "# " + title
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", fmt.Errorf("knowledge object body must contain one H1")
}
