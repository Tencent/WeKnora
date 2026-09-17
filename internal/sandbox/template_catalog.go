package sandbox

import (
	"context"
	"strings"
)

const (
	// StandardTemplateName is the provider-side name of the CLI template.
	StandardTemplateName = "weknora"

	// DesktopTemplateName is the provider-side name of the desktop template.
	// It is a sibling of StandardTemplateName, not a replacement: a config
	// uses one or the other, decided by Config.DesktopEnabled.
	DesktopTemplateName = "weknora-desktop"
)

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
	// Desktop marks the XFCE sibling of Standard. The two coexist in one
	// cluster; the admin picks which ID this config boots. Not a second
	// boot-target field on the config.
	Desktop bool `json:"desktop,omitempty"`
	// Error carries the provider's own explanation for a failed build. Without
	// it a failed template is a red badge with no way to tell a registry
	// credential problem from an out-of-disk node.
	Error string `json:"error,omitempty"`

	// Cube reports these on GET /templates; other backends leave them empty
	// and the settings list simply omits the corresponding rows.
	InstanceType        string `json:"instance_type,omitempty"`
	NetworkType         string `json:"network_type,omitempty"`
	AllowInternetAccess *bool  `json:"allow_internet_access,omitempty"`
}

// RemoteTemplateCatalog is an optional provider capability used by the
// configuration flow. It stays separate from RemoteSandboxClient because the
// session lifecycle never needs template administration.
type RemoteTemplateCatalog interface {
	ListTemplates(ctx context.Context) ([]RemoteTemplate, error)
	EnsureStandardTemplate(ctx context.Context) (*RemoteTemplate, error)
	// ReplaceStandardTemplate applies the current spec to the cluster's
	// WeKnora template (DNS, image). A READY template cannot pick those up
	// any other way. It must not delete a usable template: callers persist
	// the replacement ID first, then DeleteSupersededStandardTemplates.
	ReplaceStandardTemplate(ctx context.Context) (*RemoteTemplate, error)
	// DeleteSupersededStandardTemplates removes WeKnora templates other than
	// keepID. Call only after keepID is spawnable and has been written onto
	// every config that still pointed at the previous standard template.
	DeleteSupersededStandardTemplates(ctx context.Context, keepID string) error
}

// RemoteDesktopTemplateCatalog is the desktop sibling of RemoteTemplateCatalog.
// Cube and E2B implement it; Docker does not (SupportsDesktop stays false).
type RemoteDesktopTemplateCatalog interface {
	// EnsureDesktopTemplate returns the cluster's WeKnora desktop template,
	// building it when absent.
	EnsureDesktopTemplate(ctx context.Context) (*RemoteTemplate, error)
	// ReplaceDesktopTemplate applies the current spec to the desktop
	// template. Callers persist a READY replacement first, then
	// DeleteSupersededDesktopTemplates.
	ReplaceDesktopTemplate(ctx context.Context) (*RemoteTemplate, error)
	// DeleteSupersededDesktopTemplates removes desktop templates other than
	// keepID after that ID is spawnable and stored on the config.
	DeleteSupersededDesktopTemplates(ctx context.Context, keepID string) error
}

func isStandardTemplate(name string) bool {
	return isTemplateName(name, StandardTemplateName)
}

func isDesktopTemplate(name string) bool {
	return isTemplateName(name, DesktopTemplateName)
}

func isTemplateName(name, want string) bool {
	trimmed := strings.Trim(strings.TrimSpace(name), "/")
	if strings.EqualFold(trimmed, want) {
		return true
	}
	parts := strings.Split(trimmed, "/")
	return len(parts) > 1 && strings.EqualFold(parts[len(parts)-1], want)
}

// classifyWeKnoraTemplate decides whether a catalog entry is our CLI template,
// our desktop sibling, or neither. Name wins over image: a template aliased
// weknora-desktop is desktop even if the image repository matches the CLI
// image, and a nameless Cube template falls back to the image tag.
func classifyWeKnoraTemplate(name, image string) (standard, desktop bool) {
	if isDesktopTemplate(name) {
		return false, true
	}
	if isStandardTemplate(name) {
		return true, false
	}
	if isDesktopTemplateImage(image) {
		return false, true
	}
	if isStandardTemplateImage(image) {
		return true, false
	}
	return false, false
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

func isDesktopTemplateImage(image string) bool {
	if !isStandardTemplateImage(image) {
		return false
	}
	tag := strings.ToLower(imageTag(image))
	return tag == "main-desktop" || strings.HasSuffix(tag, "-desktop") || strings.HasSuffix(tag, "-desktop-cube")
}

func imageTag(image string) string {
	ref := strings.TrimSpace(image)
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[colon+1:]
	}
	return ""
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

// IsTemplateReady reports whether a template can spawn a sandbox. Rebuild
// replacements stay listed alongside the previous READY template until this
// is true, so sessions never lose a spawnable ID mid-build.
func IsTemplateReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready", "available", "complete", "completed", "success", "succeeded":
		return true
	default:
		return false
	}
}
