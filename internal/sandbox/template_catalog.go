package sandbox

import (
	"context"
	"strings"
)

const StandardTemplateName = "weknora"

// DefaultE2BTemplateTag is the tag E2B resolves when a sandbox is created from a
// bare template name or ID. Builds must carry it to be spawnable at all.
const DefaultE2BTemplateTag = "default"

// TemplateStatusUntagged marks a template whose builds finished but which has no
// build under the tag sandbox creation resolves. It looks identical to "still
// building" in the provider's template list, yet waiting will never help: the
// template needs a new build carrying the default tag.
const TemplateStatusUntagged = "untagged"

// RemoteTemplate is the provider-neutral template projection returned to the
// settings UI. IDs remain opaque; users choose a readable name and status.
type RemoteTemplate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	Version   string `json:"version,omitempty"`
	Image     string `json:"image,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Standard  bool   `json:"standard"`
}

// RemoteTemplateCatalog is an optional provider capability used by the
// configuration flow. It stays separate from RemoteSandboxClient because the
// session lifecycle never needs template administration.
type RemoteTemplateCatalog interface {
	ListTemplates(ctx context.Context) ([]RemoteTemplate, error)
	EnsureStandardTemplate(ctx context.Context) (*RemoteTemplate, error)
}

func isStandardTemplate(name string) bool {
	trimmed := strings.Trim(strings.TrimSpace(name), "/")
	if strings.EqualFold(trimmed, StandardTemplateName) {
		return true
	}
	parts := strings.Split(trimmed, "/")
	return len(parts) > 1 && strings.EqualFold(parts[len(parts)-1], StandardTemplateName)
}
