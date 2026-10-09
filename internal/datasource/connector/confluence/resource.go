package confluence

import (
	"fmt"
	"strings"
)

type resourceKind uint8

const (
	resourceSpace resourceKind = iota
	resourcePage
)

// resourceRef is the canonical, persisted representation of a selection.
// Space IDs deliberately remain unprefixed for backwards compatibility.
type resourceRef struct {
	Kind    resourceKind
	SpaceID string
	PageID  string
}

func makePageResourceID(spaceID, pageID string) string {
	return "page:" + spaceID + ":" + pageID
}

func parseResourceID(id string) (resourceRef, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return resourceRef{}, fmt.Errorf("invalid empty Confluence resource ID")
	}
	if !strings.HasPrefix(id, "page:") {
		if strings.Contains(id, ":") {
			return resourceRef{}, fmt.Errorf("invalid Confluence space resource ID %q", id)
		}
		return resourceRef{Kind: resourceSpace, SpaceID: id}, nil
	}
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "page" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return resourceRef{}, fmt.Errorf("invalid Confluence page resource ID %q", id)
	}
	return resourceRef{Kind: resourcePage, SpaceID: parts[1], PageID: parts[2]}, nil
}
