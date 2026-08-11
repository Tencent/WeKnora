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
	// Error carries the provider's own explanation for a failed build. Without
	// it a failed template is a red badge with no way to tell a registry
	// credential problem from an out-of-disk node.
	Error string `json:"error,omitempty"`
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

// isStandardTemplateImage recognises our template by the image it was built
// from. Names are the primary key, but a provider that drops them — Cube omits
// the field entirely when a template carries no alias — would otherwise make
// every catalog refresh look at a cluster with no standard template and build
// yet another one.
func isStandardTemplateImage(image string) bool {
	candidate := normalizeImageRepository(image)
	return candidate != "" && candidate == normalizeImageRepository(DefaultDockerImage)
}

// normalizeImageRepository reduces an image reference to its repository path so
// that "docker.io/wechatopenai/weknora-sandbox:latest",
// "wechatopenai/weknora-sandbox@sha256:…" and the bare name all compare equal.
func normalizeImageRepository(image string) string {
	ref := strings.TrimSpace(image)
	if ref == "" {
		return ""
	}
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	// A colon before the last slash belongs to a registry port, not a tag.
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		ref = ref[:colon]
	}
	ref = strings.Trim(ref, "/")
	parts := strings.Split(ref, "/")
	// Registry hosts are recognisable by a dot, a port, or being "localhost";
	// anything else at the head is a namespace we must keep.
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		parts = parts[1:]
	}
	if len(parts) > 1 && strings.EqualFold(parts[0], "library") {
		parts = parts[1:]
	}
	return strings.ToLower(strings.Join(parts, "/"))
}

// IsTemplateBuildFailed reports whether a template's build ended in a state no
// amount of waiting will improve. Such a template must be rebuilt rather than
// treated as an existing standard template.
func IsTemplateBuildFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "failure", "error", "cancelled", "canceled", TemplateStatusUntagged:
		return true
	default:
		return false
	}
}
